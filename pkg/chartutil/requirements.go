/*
Copyright 2016 The Kubernetes Authors All rights reserved.
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package chartutil

import (
	"errors"
	"log"
	"strings"
	"time"

	"github.com/ghodss/yaml"
	"k8s.io/helm/pkg/proto/hapi/chart"
)

const (
	requirementsName = "requirements.yaml"
	lockfileName     = "requirements.lock"
)

var (
	// ErrRequirementsNotFound indicates that a requirements.yaml is not found.
	ErrRequirementsNotFound = errors.New(requirementsName + " not found")
	// ErrLockfileNotFound indicates that a requirements.lock is not found.
	ErrLockfileNotFound = errors.New(lockfileName + " not found")
)

// Dependency describes a chart upon which another chart depends.
//
// Dependencies can be used to express developer intent, or to capture the state
// of a chart.
type Dependency struct {
	// Name is the name of the dependency.
	//
	// This must mach the name in the dependency's Chart.yaml.
	Name string `json:"name"`
	// Version is the version (range) of this chart.
	//
	// A lock file will always produce a single version, while a dependency
	// may contain a semantic version range.
	Version string `json:"version,omitempty"`
	// The URL to the repository.
	//
	// Appending `index.yaml` to this string should result in a URL that can be
	// used to fetch the repository index.
	Repository string `json:"repository"`
	// A yaml path that resolves to a boolean, used for enabling/disabling charts (e.g. subchart1.enabled )
	Condition string `json:"condition"`
	// Tags can be used to group charts for enabling/disabling together
	Tags []string `json:"tags"`
	// Enabled bool determines if chart should be loaded
	Enabled bool `json:"enabled"`
	// ImportValues holds the mapping of source values to parent key to be imported. Each item can be a
	// string or pair of child/parent sublist items.
	ImportValues []interface{} `json:"import-values"`
}

// ErrNoRequirementsFile to detect error condition
type ErrNoRequirementsFile error

// Requirements is a list of requirements for a chart.
//
// Requirements are charts upon which this chart depends. This expresses
// developer intent.
type Requirements struct {
	Dependencies []*Dependency `json:"dependencies"`
}

// RequirementsLock is a lock file for requirements.
//
// It represents the state that the dependencies should be in.
type RequirementsLock struct {
	// Genderated is the date the lock file was last generated.
	Generated time.Time `json:"generated"`
	// Digest is a hash of the requirements file used to generate it.
	Digest string `json:"digest"`
	// Dependencies is the list of dependencies that this lock file has locked.
	Dependencies []*Dependency `json:"dependencies"`
}

// LoadRequirements loads a requirements file from an in-memory chart.
func LoadRequirements(c *chart.Chart) (*Requirements, error) {
	var data []byte
	for _, f := range c.Files {
		if f.TypeUrl == requirementsName {
			data = f.Value
		}
	}
	if len(data) == 0 {
		return nil, ErrRequirementsNotFound
	}
	r := &Requirements{}
	return r, yaml.Unmarshal(data, r)
}

// LoadRequirementsLock loads a requirements lock file.
func LoadRequirementsLock(c *chart.Chart) (*RequirementsLock, error) {
	var data []byte
	for _, f := range c.Files {
		if f.TypeUrl == lockfileName {
			data = f.Value
		}
	}
	if len(data) == 0 {
		return nil, ErrLockfileNotFound
	}
	r := &RequirementsLock{}
	return r, yaml.Unmarshal(data, r)
}

// ProcessRequirementsConditions disables charts based on condition path value in values
func ProcessRequirementsConditions(reqs *Requirements, cvals Values) {
	var cond string
	var conds []string
	if reqs == nil || len(reqs.Dependencies) == 0 {
		return
	}
	for _, r := range reqs.Dependencies {
		var hasTrue, hasFalse bool
		cond = string(r.Condition)
		// check for list
		if len(cond) > 0 {
			if strings.Contains(cond, ",") {
				conds = strings.Split(strings.TrimSpace(cond), ",")
			} else {
				conds = []string{strings.TrimSpace(cond)}
			}
			for _, c := range conds {
				if len(c) > 0 {
					// retrieve value
					vv, err := cvals.PathValue(c)
					if err == nil {
						// if not bool, warn
						if bv, ok := vv.(bool); ok {
							if bv {
								hasTrue = true
							} else {
								hasFalse = true
							}
						} else {
							log.Printf("Warning: Condition path '%s' for chart %s returned non-bool value", c, r.Name)
						}
					} else if _, ok := err.(ErrNoValue); !ok {
						// this is a real error
						log.Printf("Warning: PathValue returned error %v", err)

					}
					if vv != nil {
						// got first value, break loop
						break
					}
				}
			}
			if !hasTrue && hasFalse {
				r.Enabled = false
			} else if hasTrue {
				r.Enabled = true

			}
		}

	}

}

// ProcessRequirementsTags disables charts based on tags in values
func ProcessRequirementsTags(reqs *Requirements, cvals Values) {
	vt, err := cvals.Table("tags")
	if err != nil {
		return

	}
	if reqs == nil || len(reqs.Dependencies) == 0 {
		return
	}
	for _, r := range reqs.Dependencies {
		if len(r.Tags) > 0 {
			tags := r.Tags

			var hasTrue, hasFalse bool
			for _, k := range tags {
				if b, ok := vt[k]; ok {
					// if not bool, warn
					if bv, ok := b.(bool); ok {
						if bv {
							hasTrue = true
						} else {
							hasFalse = true
						}
					} else {
						log.Printf("Warning: Tag '%s' for chart %s returned non-bool value", k, r.Name)
					}
				}
			}
			if !hasTrue && hasFalse {
				r.Enabled = false
			} else if hasTrue || !hasTrue && !hasFalse {
				r.Enabled = true

			}

		}
	}

}

// ProcessRequirementsEnabled removes disabled charts from dependencies
func ProcessRequirementsEnabled(c *chart.Chart, v *chart.Config) error {
	reqs, err := LoadRequirements(c)
	if err != nil {
		// if not just missing requirements file, return error
		if nerr, ok := err.(ErrNoRequirementsFile); !ok {
			return nerr
		}

		// no requirements to process
		return nil
	}
	// set all to true
	for _, lr := range reqs.Dependencies {
		lr.Enabled = true
	}
	cvals, err := CoalesceValues(c, v)
	if err != nil {
		return err
	}
	// flag dependencies as enabled/disabled
	ProcessRequirementsTags(reqs, cvals)
	ProcessRequirementsConditions(reqs, cvals)

	// make a map of charts to remove
	rm := map[string]bool{}
	for _, r := range reqs.Dependencies {
		if !r.Enabled {
			// remove disabled chart
			rm[r.Name] = true
		}
	}
	// don't keep disabled charts in new slice
	cd := []*chart.Chart{}
	copy(cd, c.Dependencies[:0])
	for _, n := range c.Dependencies {
		if _, ok := rm[n.Metadata.Name]; !ok {
			cd = append(cd, n)
		}

	}
	// recursively call self to process sub dependencies
	for _, t := range cd {
		err := ProcessRequirementsEnabled(t, v)
		// if its not just missing requirements file, return error
		if nerr, ok := err.(ErrNoRequirementsFile); !ok && err != nil {
			return nerr
		}
	}
	c.Dependencies = cd

	return nil
}

// pathToMap creates a nested map given a YAML path in dot notation.
func pathToMap(path string, data map[string]interface{}) map[string]interface{} {
	if path == "." {
		return data
	}
	ap := strings.Split(path, ".")
	if len(ap) == 0 {
		return nil
	}
	n := []map[string]interface{}{}
	// created nested map for each key, adding to slice
	for _, v := range ap {
		nm := make(map[string]interface{})
		nm[v] = make(map[string]interface{})
		n = append(n, nm)
	}
	// find the last key (map) and set our data
	for i, d := range n {
		for k := range d {
			z := i + 1
			if z == len(n) {
				n[i][k] = data
				break
			}
			n[i][k] = n[z]
		}
	}

	return n[0]
}

// getParents returns a slice of parent charts in reverse order.
func getParents(c *chart.Chart, out []*chart.Chart) []*chart.Chart {
	if len(out) == 0 {
		out = []*chart.Chart{c}
	}
	for _, ch := range c.Dependencies {
		if len(ch.Dependencies) > 0 {
			out = append(out, ch)
			out = getParents(ch, out)
		}
	}

	return out
}

// processImportValues merges values from child to parent based on the chart's dependencies' ImportValues field.
func processImportValues(c *chart.Chart, v *chart.Config) error {
	reqs, err := LoadRequirements(c)
	if err != nil {
		return err
	}
	// combine chart values and its dependencies' values
	cvals, err := CoalesceValues(c, v)
	if err != nil {
		return err
	}
	nv := v.GetValues()
	b := make(map[string]interface{}, len(nv))
	// convert values to map
	for kk, vvv := range nv {
		b[kk] = vvv
	}
	// import values from each dependency if specified in import-values
	for _, r := range reqs.Dependencies {
		if len(r.ImportValues) > 0 {
			var outiv []interface{}
			for _, riv := range r.ImportValues {
				switch iv := riv.(type) {
				case map[string]interface{}:
					nm := map[string]string{
						"child":  iv["child"].(string),
						"parent": iv["parent"].(string),
					}
					outiv = append(outiv, nm)
					s := r.Name + "." + nm["child"]
					// get child table
					vv, err := cvals.Table(s)
					if err != nil {
						log.Printf("Warning: ImportValues missing table: %v", err)
						continue
					}
					// create value map from child to be merged into parent
					vm := pathToMap(nm["parent"], vv.AsMap())
					b = coalesceTables(cvals, vm)
				case string:
					nm := map[string]string{
						"child":  "exports." + iv,
						"parent": ".",
					}
					outiv = append(outiv, nm)
					s := r.Name + "." + nm["child"]
					vm, err := cvals.Table(s)
					if err != nil {
						log.Printf("Warning: ImportValues missing table: %v", err)
						continue
					}
					b = coalesceTables(b, vm.AsMap())
				}
			}
			// set our formatted import values
			r.ImportValues = outiv
		}
	}
	b = coalesceTables(b, cvals)
	y, err := yaml.Marshal(b)
	if err != nil {
		return err
	}
	// set the new values
	c.Values.Raw = string(y)

	return nil
}

// ProcessRequirementsImportValues imports specified chart values from child to parent.
func ProcessRequirementsImportValues(c *chart.Chart, v *chart.Config) error {
	pc := getParents(c, nil)
	for i := len(pc) - 1; i >= 0; i-- {
		processImportValues(pc[i], v)
	}

	return nil
}
