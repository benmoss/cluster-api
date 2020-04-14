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
	controlplanev1 "sigs.k8s.io/cluster-api/controlplane/kubeadm/api/v1alpha3"
	"sigs.k8s.io/cluster-api/controlplane/kubeadm/internal/hash"
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
				machine("machine-4", withCreationTimestamp(time.Date(2018, 04, 02, 03, 04, 05, 06, time.UTC))),
				machine("machine-5", withCreationTimestamp(time.Date(2018, 05, 02, 03, 04, 05, 06, time.UTC))),
				machine("machine-2", withCreationTimestamp(time.Date(2018, 02, 02, 03, 04, 05, 06, time.UTC))),
				machine("machine-1", withCreationTimestamp(time.Date(2018, 01, 02, 03, 04, 05, 06, time.UTC))),
				machine("machine-3", withCreationTimestamp(time.Date(2018, 03, 02, 03, 04, 05, 06, time.UTC))),
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

	Describe("DefaultScaleStrategy", func() {
		var (
			startDate = time.Date(2000, 1, 1, 1, 0, 0, 0, time.UTC)
		)
		Context("non-failure domain", func() {
			It("scales up when machines are past the upgradeAfter time", func() {
				kcp := controlplanev1.KubeadmControlPlaneSpec{
					Replicas:     pointer.Int32Ptr(2),
					UpgradeAfter: &metav1.Time{Time: startDate},
				}
				machines := NewFilterableMachineCollection(
					machine("machine-1", withCreationTimestamp(startDate.Add(-time.Hour)), withValidHash(kcp)),
					machine("machine-2", withCreationTimestamp(startDate.Add(-2*time.Hour)), withValidHash(kcp)),
				)
				strategy := NewDefaultScaleStrategy(
					kcp,
					NewFilterableMachineCollectionByFailureDomain(
						nil,
						machines,
					),
				)
				Expect(strategy.NeedsScaleUp()).To(BeTrue())
				Expect(strategy.NeedsScaleDown()).To(BeFalse())
				Expect(strategy.NextForScaleDown()).To(Equal(machines.Filter(func(m *clusterv1.Machine) bool {
					return m.ObjectMeta.Name == "machine-2"
				}).Items()[0]))
			})

			It("scales up when the machines hash no longer match the KCP spec hash", func() {
				kcp := controlplanev1.KubeadmControlPlaneSpec{
					Replicas: pointer.Int32Ptr(3),
				}
				machines := NewFilterableMachineCollection(
					machine("machine-1", withHash("some-wrong-hash"), withCreationTimestamp(startDate)),
					machine("machine-2", withValidHash(kcp)),
					machine("machine-3", withHash("some-wrong-hash"), withCreationTimestamp(startDate.Add(-time.Hour))),
				)
				strategy := NewDefaultScaleStrategy(kcp, NewFilterableMachineCollectionByFailureDomain(nil, machines))
				Expect(strategy.NeedsScaleUp()).To(BeTrue())
				Expect(strategy.NeedsScaleDown()).To(BeFalse())
				Expect(strategy.NextForScaleDown()).To(Equal(machines.Filter(func(m *clusterv1.Machine) bool {
					return m.ObjectMeta.Name == "machine-3"
				}).Items()[0]))
			})

			It("scales up when number of machines < number of replicas", func() {
				kcp := controlplanev1.KubeadmControlPlaneSpec{
					Replicas: pointer.Int32Ptr(2),
				}
				machines := NewFilterableMachineCollection(
					machine("machine-1", withCreationTimestamp(startDate), withValidHash(kcp)),
				)
				strategy := NewDefaultScaleStrategy(kcp, NewFilterableMachineCollectionByFailureDomain(nil, machines))
				Expect(strategy.NeedsScaleUp()).To(BeTrue())
				Expect(strategy.NeedsScaleDown()).To(BeFalse())
			})

			It("does not scale up when there are outdated machines but already too many replicas", func() {
				kcp := controlplanev1.KubeadmControlPlaneSpec{
					Replicas: pointer.Int32Ptr(1),
				}
				machines := NewFilterableMachineCollection(
					machine("machine-1", withHash("some-wrong-hash"), withCreationTimestamp(startDate.Add(-time.Hour))),
					machine("machine-2", withValidHash(kcp), withCreationTimestamp(startDate)),
				)
				strategy := NewDefaultScaleStrategy(kcp, NewFilterableMachineCollectionByFailureDomain(nil, machines))
				Expect(strategy.NeedsScaleUp()).To(BeFalse())
				Expect(strategy.NeedsScaleDown()).To(BeTrue())
				Expect(strategy.NextForScaleDown()).To(Equal(machines.Filter(func(m *clusterv1.Machine) bool {
					return m.ObjectMeta.Name == "machine-1"
				}).Items()[0]))
			})

			It("scales down when number of machines > number of replicas", func() {
				kcp := controlplanev1.KubeadmControlPlaneSpec{
					Replicas: pointer.Int32Ptr(1),
				}
				machines := NewFilterableMachineCollection(
					machine("machine-1", withCreationTimestamp(startDate), withValidHash(kcp)),
					machine("machine-2", withCreationTimestamp(startDate.Add(-time.Hour)), withValidHash(kcp)),
				)
				strategy := NewDefaultScaleStrategy(kcp, NewFilterableMachineCollectionByFailureDomain(nil, machines))
				Expect(strategy.NeedsScaleUp()).To(BeFalse())
				Expect(strategy.NeedsScaleDown()).To(BeTrue())
				Expect(strategy.NextForScaleDown()).To(Equal(machines.Filter(func(m *clusterv1.Machine) bool {
					return m.ObjectMeta.Name == "machine-2"
				}).Items()[0]))
			})
		})

		Context("failure domain", func() {
			It("scales down the failure domain with the most number of outdated machines first", func() {
				kcp := controlplanev1.KubeadmControlPlaneSpec{
					UpgradeAfter: &metav1.Time{Time: startDate},
					Replicas:     pointer.Int32Ptr(5),
				}
				// FD 1 is larger, but FD 2 has two outdated, compared with only one in 1.
				// machine-3 is oldest overall, but 5 is chosen because its oldest in FD 2
				machines := NewFilterableMachineCollection(
					machine("machine-1", withCreationTimestamp(startDate), withFailureDomain("one"), withValidHash(kcp)),
					machine("machine-2", withCreationTimestamp(startDate), withFailureDomain("one"), withValidHash(kcp)),
					machine("machine-3", withCreationTimestamp(startDate.Add(5*-time.Hour)), withFailureDomain("one"), withValidHash(kcp)),
					machine("machine-4", withCreationTimestamp(startDate.Add(2*-time.Hour)), withFailureDomain("two"), withValidHash(kcp)),
					machine("machine-5", withCreationTimestamp(startDate.Add(3*-time.Hour)), withFailureDomain("two"), withValidHash(kcp)),
				)
				strategy := NewDefaultScaleStrategy(kcp,
					NewFilterableMachineCollectionByFailureDomain(clusterv1.FailureDomains{
						"one": failureDomain(true),
						"two": failureDomain(true),
					}, machines),
				)
				Expect(strategy.NeedsScaleUp()).To(BeTrue())
				Expect(strategy.NeedsScaleDown()).To(BeFalse())
				Expect(strategy.NextForScaleDown()).To(Equal(machines.Filter(func(m *clusterv1.Machine) bool {
					return m.ObjectMeta.Name == "machine-5"
				}).Items()[0]))
			})

			It("scales down the machines in unknown failure domains before known failure domains", func() {
				kcp := controlplanev1.KubeadmControlPlaneSpec{
					Replicas: pointer.Int32Ptr(1),
				}
				machines := NewFilterableMachineCollection(
					// oldest, but in known domain
					machine("machine-1", withCreationTimestamp(startDate.Add(4*-time.Hour)), withFailureDomain("one"), withValidHash(kcp)),
					machine("machine-2", withCreationTimestamp(startDate.Add(3*-time.Hour)), withFailureDomain("one"), withValidHash(kcp)),
					// oldest in unknown domain
					machine("machine-3", withCreationTimestamp(startDate.Add(2*-time.Hour)), withFailureDomain("shrug"), withValidHash(kcp)),
					machine("machine-4", withCreationTimestamp(startDate.Add(1*-time.Hour)), withValidHash(kcp)),
				)
				strategy := NewDefaultScaleStrategy(kcp,
					NewFilterableMachineCollectionByFailureDomain(clusterv1.FailureDomains{
						"one": failureDomain(true),
					}, machines),
				)
				Expect(strategy.NeedsScaleUp()).To(BeFalse())
				Expect(strategy.NeedsScaleDown()).To(BeTrue())
				Expect(strategy.NextForScaleDown()).To(Equal(machines.Filter(func(m *clusterv1.Machine) bool {
					return m.ObjectMeta.Name == "machine-3"
				}).Items()[0]))
			})
		})
	})
})

/* Helper functions to build machine objects for tests */

type machineOpt func(*clusterv1.Machine)

func withCreationTimestamp(timestamp time.Time) machineOpt {
	return func(m *clusterv1.Machine) {
		m.CreationTimestamp = metav1.Time{Time: timestamp}
	}
}

func withValidHash(kcp controlplanev1.KubeadmControlPlaneSpec) machineOpt {
	return func(m *clusterv1.Machine) {
		withHash(hash.Compute(&kcp))(m)
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
