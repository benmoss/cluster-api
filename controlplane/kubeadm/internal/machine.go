package internal

import (
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1alpha3"
	bootstrapv1 "sigs.k8s.io/cluster-api/bootstrap/kubeadm/api/v1alpha3"
	"sigs.k8s.io/cluster-api/controlplane/kubeadm/internal/machinefilters"
)

// Machine is a wrapper for clusterv1.Machine and its related resources
type Machine struct {
	Machine       *clusterv1.Machine
	InfraMachine  *unstructured.Unstructured
	KubeadmConfig *bootstrapv1.KubeadmConfig
}

type MachineCollection []*Machine

// Filter returns a FilterableMachineCollection containing only the Machines that match all of the given MachineFilters
func (c MachineCollection) Filter(filters ...machinefilters.Func) FilterableMachineCollection {
	result := FilterableMachineCollection{}
	for _, machine := range c {
		if machinefilters.And(filters...)(machine.Machine) {
			result.Insert(machine.Machine)
		}
	}
	return result
}

// Difference returns a FilterableMachineCollection without machines that are in the given collection
func (c MachineCollection) Difference(machines FilterableMachineCollection) FilterableMachineCollection {
	return c.Filter(func(m *clusterv1.Machine) bool {
		_, found := machines[m.Name]
		return !found
	})
}
