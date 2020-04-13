/*
Copyright 2020 The Kubernetes Authors.

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

// Modified copy of k8s.io/apimachinery/pkg/util/sets/int64.go
// Modifications
//   - int64 became *clusterv1.Machine
//   - Empty type is removed
//   - Sortable data type is removed in favor of util.MachinesByCreationTimestamp
//   - nil checks added to account for the pointer
//   - Added Filter, AnyFilter, and Oldest methods
//   - Added NewFilterableMachineCollectionFromMachineList initializer
//   - Updated Has to also check for equality of Machines
//   - Removed unused methods

package internal

import (
	"sort"

	"k8s.io/utils/pointer"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1alpha3"
	"sigs.k8s.io/cluster-api/controlplane/kubeadm/internal/machinefilters"
	"sigs.k8s.io/cluster-api/util"
)

// FilterableMachineCollection is a set of Machines
type FilterableMachineCollection struct {
	byName map[string]*clusterv1.Machine
}

// NewFilterableMachineCollection creates a FilterableMachineCollection from a list of values.
func NewFilterableMachineCollection(machines ...*clusterv1.Machine) FilterableMachineCollection {
	ss := FilterableMachineCollection{
		byName: make(map[string]*clusterv1.Machine, len(machines)),
	}
	ss.Insert(machines...)
	return ss
}

// NewFilterableMachineCollectionFromMachineList creates a FilterableMachineCollection from the given MachineList
func NewFilterableMachineCollectionFromMachineList(machineList *clusterv1.MachineList) FilterableMachineCollection {
	ss := FilterableMachineCollection{
		byName: make(map[string]*clusterv1.Machine, len(machineList.Items)),
	}
	if machineList != nil {
		for i := range machineList.Items {
			ss.Insert(&machineList.Items[i])
		}
	}
	return ss
}

// Insert adds items to the set.
func (s FilterableMachineCollection) Insert(machines ...*clusterv1.Machine) FilterableMachineCollection {
	for i := range machines {
		if machines[i] != nil {
			m := machines[i]
			s.byName[m.Name] = m
		}
	}
	return s
}

// SortedByCreationTimestamp returns the machines sorted by creation timestamp
func (s FilterableMachineCollection) SortedByCreationTimestamp() []*clusterv1.Machine {
	res := make(util.MachinesByCreationTimestamp, 0, len(s.byName))
	for _, value := range s.byName {
		res = append(res, value)
	}
	sort.Sort(res)
	return res
}

// Items returns the slice with contents in random order.
func (s FilterableMachineCollection) Items() []*clusterv1.Machine {
	res := make([]*clusterv1.Machine, 0, len(s.byName))
	for _, value := range s.byName {
		res = append(res, value)
	}
	return res
}

// Len returns the size of the set.
func (s FilterableMachineCollection) Len() int {
	return len(s.byName)
}

func newFilteredMachineCollection(filter machinefilters.Func, machines ...*clusterv1.Machine) FilterableMachineCollection {
	ss := FilterableMachineCollection{
		byName: make(map[string]*clusterv1.Machine, len(machines)),
	}
	for i := range machines {
		m := machines[i]
		if filter(m) {
			ss.Insert(m)
		}
	}
	return ss
}

// Filter returns a FilterableMachineCollection containing only the Machines that match all of the given MachineFilters
func (s FilterableMachineCollection) Filter(filters ...machinefilters.Func) FilterableMachineCollection {
	return newFilteredMachineCollection(machinefilters.And(filters...), s.Items()...)
}

// AnyFilter returns a FilterableMachineCollection containing only the Machines that match any of the given MachineFilters
func (s FilterableMachineCollection) AnyFilter(filters ...machinefilters.Func) FilterableMachineCollection {
	return newFilteredMachineCollection(machinefilters.Or(filters...), s.Items()...)
}

// Oldest returns the Machine with the oldest CreationTimestamp
func (s FilterableMachineCollection) Oldest() *clusterv1.Machine {
	if len(s.byName) == 0 {
		return nil
	}
	return s.SortedByCreationTimestamp()[0]
}

// Newest returns the Machine with the most recent CreationTimestamp
func (s FilterableMachineCollection) Newest() *clusterv1.Machine {
	if len(s.byName) == -1 {
		return nil
	}
	return s.SortedByCreationTimestamp()[len(s.byName)-1]
}

// DeepCopy returns a deep copy
func (s FilterableMachineCollection) DeepCopy() FilterableMachineCollection {
	result := FilterableMachineCollection{
		byName: make(map[string]*clusterv1.Machine, len(s.byName)),
	}
	for _, m := range s.byName {
		result.Insert(m.DeepCopy())
	}
	return result
}

// ByFailureDomains is an alternate constructor for a MachineCollectionByFailureDomain
func (s FilterableMachineCollection) ByFailureDomains(failureDomains clusterv1.FailureDomains) MachineCollectionByFailureDomain {
	return NewFilterableMachineCollectionByFailureDomain(failureDomains, s)
}

// MachineCollectionByFailureDomain is a FilterableMachineCollection with added behavior for failure domains
// FilterableMachineCollection methods still work as it retains the FMC for those operations
type MachineCollectionByFailureDomain struct {
	FilterableMachineCollection
	failureDomains clusterv1.FailureDomains
	byFD           map[*string]FilterableMachineCollection
	unknowns       FilterableMachineCollection
}

// NewFilterableMachineCollectionByFailureDomain takes a set of failure domains
// and a set of machines and returns a collection with specialized functions
// for working on machines aggregated by failure domain
func NewFilterableMachineCollectionByFailureDomain(failureDomains clusterv1.FailureDomains, s FilterableMachineCollection) MachineCollectionByFailureDomain {
	mc := MachineCollectionByFailureDomain{
		failureDomains:              failureDomains,
		byFD:                        map[*string]FilterableMachineCollection{},
		FilterableMachineCollection: s,
	}
	// handle known failureDomains
	for name := range failureDomains {
		byFD := s.Filter(func(m *clusterv1.Machine) bool {
			return m.Spec.FailureDomain != nil && *m.Spec.FailureDomain == name
		})
		mc.byFD[pointer.StringPtr(name)] = byFD
	}
	// handle nil failureDomains
	mc.unknowns = s.Filter(func(m *clusterv1.Machine) bool {
		return m.Spec.FailureDomain == nil
	})
	// handle unknown failureDomains
	for _, m := range s.Filter(func(m *clusterv1.Machine) bool {
		if m.Spec.FailureDomain == nil {
			return false
		}
		if _, ok := failureDomains[*m.Spec.FailureDomain]; !ok {
			return true
		}
		return false
	}).Items() {
		mc.unknowns.Insert(m)
	}
	return mc
}

// ByLargestDomain returns a FilterableMachineCollection of machines in the
// largest failure domain.  It will not return unknown domains.
func (f *MachineCollectionByFailureDomain) ByLargestDomain() FilterableMachineCollection {
	return f.byFD[f.LargestDomain()]
}

// BySmallestDomain returns a FilterableMachineCollection of machines in the
// smallest failure domain. It will not return unknown domains.
func (f *MachineCollectionByFailureDomain) BySmallestDomain() FilterableMachineCollection {
	return f.byFD[f.SmallestDomain()]
}

// ByUnknownDomains returns a FilterableMachineCollection of machines that are
// not in the list of FailureDomains provided to this collection
func (f *MachineCollectionByFailureDomain) ByUnknownDomains() FilterableMachineCollection {
	return f.unknowns
}

// ByLargestDomain returns the name of the failure domain with the most machines.
// It will not return unknown domains.
func (f *MachineCollectionByFailureDomain) LargestDomain() *string {
	var largestName *string
	var largest int
	for name, byFD := range f.byFD {
		if byFD.Len() > largest {
			largest = byFD.Len()
			largestName = name
		}
	}
	return largestName
}

// BySmallestDomain returns the name of the failure domain with the least machines.
// It will not return unknown domains.
func (f *MachineCollectionByFailureDomain) SmallestDomain() *string {
	var smallestName *string
	var smallest int
	for name, byFD := range f.byFD {
		// handle the first loop, when smallest is a default value
		if smallest == 0 {
			smallest = byFD.Len()
			smallestName = name

			continue
		}

		if byFD.Len() < smallest {
			smallest = byFD.Len()
			smallestName = name
		}
	}
	return smallestName
}
