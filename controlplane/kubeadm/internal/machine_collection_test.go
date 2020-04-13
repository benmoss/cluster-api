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

package internal

import (
	"testing"
	"time"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/pointer"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1alpha3"
)

func TestMachineCollection(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Machine Collection Suite")
}

var _ = Describe("Machine Collection", func() {
	Describe("FilterableMachineCollection", func() {
		var collection FilterableMachineCollection
		BeforeEach(func() {
			collection = NewFilterableMachineCollection(
				machine("machine-4", withCreationTimestamp(metav1.Time{Time: time.Date(2018, 04, 02, 03, 04, 05, 06, time.UTC)})),
				machine("machine-5", withCreationTimestamp(metav1.Time{Time: time.Date(2018, 05, 02, 03, 04, 05, 06, time.UTC)})),
				machine("machine-2", withCreationTimestamp(metav1.Time{Time: time.Date(2018, 02, 02, 03, 04, 05, 06, time.UTC)})),
				machine("machine-1", withCreationTimestamp(metav1.Time{Time: time.Date(2018, 01, 02, 03, 04, 05, 06, time.UTC)})),
				machine("machine-3", withCreationTimestamp(metav1.Time{Time: time.Date(2018, 03, 02, 03, 04, 05, 06, time.UTC)})),
			)
		})
		Context("SortedByAge", func() {
			It("should return the same number of machines as are in the collection", func() {
				sortedMachines := collection.SortedByCreationTimestamp()
				Expect(sortedMachines).To(HaveLen(collection.Len()))
				Expect(sortedMachines[0].Name).To(Equal("machine-1"))
				Expect(sortedMachines[len(sortedMachines)-1].Name).To(Equal("machine-5"))
			})
		})
	})

	Describe("MachineCollectionByFailureDomain", func() {
		It("allows access by failure domain", func() {
			mc := NewFilterableMachineCollectionByFailureDomain(
				clusterv1.FailureDomains{
					"one": clusterv1.FailureDomainSpec{ControlPlane: true},
					"two": clusterv1.FailureDomainSpec{ControlPlane: true},
				},
				NewFilterableMachineCollection(
					machine("machine-1", withFailureDomain("one")),
					machine("machine-2", withFailureDomain("one")),
					machine("machine-3", withFailureDomain("two")),
				),
			)
			Expect(mc.ByLargestDomain()).To(Equal(mc.Filter(func(m *clusterv1.Machine) bool {
				return m.ObjectMeta.Name == "machine-1" || m.ObjectMeta.Name == "machine-2"
			})))
			Expect(mc.BySmallestDomain()).To(Equal(mc.Filter(func(m *clusterv1.Machine) bool {
				return m.ObjectMeta.Name == "machine-3"
			})))
			Expect(mc.LargestDomain()).To(Equal(pointer.StringPtr("one")))
			Expect(mc.SmallestDomain()).To(Equal(pointer.StringPtr("two")))
		})

		It("keeps unknown and nil domains separate", func() {
			mc := NewFilterableMachineCollectionByFailureDomain(
				clusterv1.FailureDomains{
					"one": clusterv1.FailureDomainSpec{ControlPlane: true},
					"two": clusterv1.FailureDomainSpec{ControlPlane: true},
				},
				NewFilterableMachineCollection(
					machine("machine-1", withFailureDomain("one")),
					machine("machine-2", withFailureDomain("one")),
					machine("machine-3", withFailureDomain("two")),
					machine("machine-4", withFailureDomain("got me")),
					machine("machine-5", withFailureDomain("who knows")),
					machine("machine-6"),
				),
			)
			Expect(mc.ByLargestDomain()).To(Equal(mc.Filter(func(m *clusterv1.Machine) bool {
				return m.ObjectMeta.Name == "machine-1" || m.ObjectMeta.Name == "machine-2"
			})))
			Expect(mc.BySmallestDomain()).To(Equal(mc.Filter(func(m *clusterv1.Machine) bool {
				return m.ObjectMeta.Name == "machine-3"
			})))
			Expect(mc.LargestDomain()).To(Equal(pointer.StringPtr("one")))
			Expect(mc.SmallestDomain()).To(Equal(pointer.StringPtr("two")))
			Expect(mc.ByUnknownDomains()).To(Equal(mc.Filter(func(m *clusterv1.Machine) bool {
				return m.ObjectMeta.Name == "machine-4" || m.ObjectMeta.Name == "machine-5" || m.ObjectMeta.Name == "machine-6"
			})))
		})
	})
})

/* Helper functions to build machine objects for tests */

type machineOpt func(*clusterv1.Machine)

func withCreationTimestamp(timestamp metav1.Time) machineOpt {
	return func(m *clusterv1.Machine) {
		m.CreationTimestamp = timestamp
	}
}

func machine(name string, opts ...machineOpt) *clusterv1.Machine {
	m := &clusterv1.Machine{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
	}
	for _, opt := range opts {
		opt(m)
	}
	return m
}
