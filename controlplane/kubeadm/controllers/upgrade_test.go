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

package controllers

import (
	"context"
	"testing"
	"time"

	. "github.com/onsi/gomega"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/record"
	"k8s.io/utils/pointer"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1alpha3"
	controlplanev1 "sigs.k8s.io/cluster-api/controlplane/kubeadm/api/v1alpha3"
	"sigs.k8s.io/cluster-api/controlplane/kubeadm/internal"
	capierrors "sigs.k8s.io/cluster-api/errors"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

func TestKubeadmControlPlaneReconciler_upgradeControlPlane(t *testing.T) {
	g := NewWithT(t)

	cluster, kcp, genericMachineTemplate := createClusterWithControlPlane()
	kcp.Spec.Version = "v1.17.3"
	kcp.Spec.KubeadmConfigSpec.ClusterConfiguration = nil
	kcp.Spec.Replicas = pointer.Int32Ptr(1)

	fakeClient := newFakeClient(g, cluster.DeepCopy(), kcp.DeepCopy(), genericMachineTemplate.DeepCopy())

	r := &KubeadmControlPlaneReconciler{
		Client:   fakeClient,
		Log:      log.Log,
		recorder: record.NewFakeRecorder(32),
		managementCluster: &fakeManagementCluster{
			Management:          &internal.Management{Client: fakeClient},
			Workload:            fakeWorkloadCluster{Status: internal.ClusterStatus{Nodes: 1}},
			ControlPlaneHealthy: true,
			EtcdHealthy:         true,
		},
	}
	controlPlane := &internal.ControlPlane{
		KCP:      kcp,
		Cluster:  cluster,
		Machines: nil,
	}

	result, err := r.initializeControlPlane(context.Background(), cluster, kcp, controlPlane)
	g.Expect(result).To(Equal(ctrl.Result{Requeue: true}))
	g.Expect(err).NotTo(HaveOccurred())

	// initial setup
	initialMachine := &clusterv1.MachineList{}
	g.Expect(fakeClient.List(context.Background(), initialMachine, client.InNamespace(cluster.Namespace))).To(Succeed())
	g.Expect(initialMachine.Items).To(HaveLen(1))

	// run upgrade the first time, expect we scale up
	needingUpgrade := internal.NewFilterableMachineCollectionFromMachineList(initialMachine)
	result, err = r.upgradeControlPlane(context.Background(), cluster, kcp, needingUpgrade, controlPlane)
	g.Expect(result).To(Equal(ctrl.Result{Requeue: true}))
	g.Expect(err).To(BeNil())
	bothMachines := &clusterv1.MachineList{}
	g.Expect(fakeClient.List(context.Background(), bothMachines, client.InNamespace(cluster.Namespace))).To(Succeed())
	g.Expect(bothMachines.Items).To(HaveLen(2))

	// run upgrade a second time, simulate that the node has not appeared yet but the machine exists
	r.managementCluster.(*fakeManagementCluster).ControlPlaneHealthy = false
	_, err = r.upgradeControlPlane(context.Background(), cluster, kcp, needingUpgrade, controlPlane)
	g.Expect(err).To(Equal(&capierrors.RequeueAfterError{RequeueAfter: healthCheckFailedRequeueAfter}))
	g.Expect(fakeClient.List(context.Background(), bothMachines, client.InNamespace(cluster.Namespace))).To(Succeed())
	g.Expect(bothMachines.Items).To(HaveLen(2))

	// manually increase number of nodes, make control plane healthy again
	r.managementCluster.(*fakeManagementCluster).Workload.Status.Nodes++
	r.managementCluster.(*fakeManagementCluster).ControlPlaneHealthy = true

	// run upgrade the second time, expect we scale down
	result, err = r.upgradeControlPlane(context.Background(), cluster, kcp, needingUpgrade, controlPlane)
	g.Expect(result).To(Equal(ctrl.Result{Requeue: true}))
	g.Expect(err).To(BeNil())
	finalMachine := &clusterv1.MachineList{}
	g.Expect(fakeClient.List(context.Background(), finalMachine, client.InNamespace(cluster.Namespace))).To(Succeed())
	g.Expect(finalMachine.Items).To(HaveLen(1))

	// assert that the deleted machine is the oldest, initial machine
	g.Expect(finalMachine.Items[0].Name).ToNot(Equal(initialMachine.Items[0].Name))
	g.Expect(finalMachine.Items[0].CreationTimestamp.Time).To(BeTemporally(">", initialMachine.Items[0].CreationTimestamp.Time))
}

func TestSelectMachineForUpgrade(t *testing.T) {
	g := NewWithT(t)

	cluster, kcp, genericMachineTemplate := createClusterWithControlPlane()
	kcp.Spec.KubeadmConfigSpec.ClusterConfiguration = nil

	m1 := machine("machine-1", withFailureDomain("one"), withTimestamp(metav1.Time{Time: time.Date(1, 0, 0, 0, 0, 0, 0, time.UTC)}))
	m2 := machine("machine-2", withFailureDomain("two"), withTimestamp(metav1.Time{Time: time.Date(2, 0, 0, 0, 0, 0, 0, time.UTC)}))
	m3 := machine("machine-3", withFailureDomain("two"), withTimestamp(metav1.Time{Time: time.Date(3, 0, 0, 0, 0, 0, 0, time.UTC)}))
	m4 := machine("machine-4", withFailureDomain("four"))

	mc1 := internal.FilterableMachineCollection{"machine-1": m1}
	mc2 := internal.FilterableMachineCollection{
		"machine-1": m1,
		"machine-2": m2,
	}
	mc3 := internal.FilterableMachineCollection{
		"machine-1": m1,
		"machine-2": m2,
		"machine-3": m3,
	}
	fd := clusterv1.FailureDomains{
		"one":   failureDomain(true),
		"two":   failureDomain(true),
		"three": failureDomain(false),
	}

	controlPlane3Machines := &internal.ControlPlane{
		KCP:      &controlplanev1.KubeadmControlPlane{},
		Cluster:  &clusterv1.Cluster{Status: clusterv1.ClusterStatus{FailureDomains: fd}},
		Machines: mc3,
	}

	fakeClient := newFakeClient(
		g,
		cluster.DeepCopy(),
		kcp.DeepCopy(),
		genericMachineTemplate.DeepCopy(),
		m1.DeepCopy(),
		m2.DeepCopy(),
		m4.DeepCopy(),
	)

	r := &KubeadmControlPlaneReconciler{
		Client:   fakeClient,
		Log:      log.Log,
		recorder: record.NewFakeRecorder(32),
		managementCluster: &fakeManagementCluster{
			Management: &internal.Management{Client: fakeClient},
			Workload:   fakeWorkloadCluster{},
		},
	}

	testCases := []struct {
		name            string
		upgradeMachines internal.FilterableMachineCollection
		cp              *internal.ControlPlane
		expectErr       bool
		expectedMachine clusterv1.Machine
	}{
		{
			name:            "When controlplane and upgrade machines are same, picks the oldest failure domain with most machines ",
			upgradeMachines: mc3,
			cp:              controlPlane3Machines,
			expectErr:       false,
			expectedMachine: clusterv1.Machine{ObjectMeta: metav1.ObjectMeta{Name: "machine-2"}},
		},
		{
			name:            "Picks the upgrade machine even if it does not belong to the fd with most machines",
			upgradeMachines: mc1,
			cp:              controlPlane3Machines,
			expectErr:       false,
			expectedMachine: clusterv1.Machine{ObjectMeta: metav1.ObjectMeta{Name: "machine-1"}},
		},
		{
			name:            "Picks the upgrade machine that belongs to the fd with most machines",
			upgradeMachines: mc2,
			cp:              controlPlane3Machines,
			expectErr:       false,
			expectedMachine: clusterv1.Machine{ObjectMeta: metav1.ObjectMeta{Name: "machine-2"}},
		},
		{
			name:            "Picks the upgrade machine that is not in existing failure domains",
			upgradeMachines: internal.FilterableMachineCollection{"machine-4": m4},
			cp:              controlPlane3Machines,
			expectErr:       false,
			expectedMachine: clusterv1.Machine{ObjectMeta: metav1.ObjectMeta{Name: "machine-4"}},
		},
		{
			name:            "Marking machine with annotation fails",
			upgradeMachines: internal.FilterableMachineCollection{"machine-3": m3},
			cp:              controlPlane3Machines,
			expectErr:       true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)

			g.Expect(clusterv1.AddToScheme(scheme.Scheme)).To(Succeed())

			selectedMachine, err := r.selectAndMarkMachine(context.Background(), tc.upgradeMachines, controlplanev1.DeleteForScaleDownAnnotation, tc.cp)

			if tc.expectErr {
				g.Expect(err).To(HaveOccurred())
				return
			}

			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(selectedMachine.Name).To(Equal(tc.expectedMachine.Name))
		})
	}

}

func failureDomain(controlPlane bool) clusterv1.FailureDomainSpec {
	return clusterv1.FailureDomainSpec{
		ControlPlane: controlPlane,
	}
}

type machineOpt func(*clusterv1.Machine)

func withFailureDomain(fd string) machineOpt {
	return func(m *clusterv1.Machine) {
		m.Spec.FailureDomain = &fd
	}
}

func withTimestamp(t metav1.Time) machineOpt {
	return func(m *clusterv1.Machine) {
		m.CreationTimestamp = t
	}
}

func machine(name string, opts ...machineOpt) *clusterv1.Machine {
	m := &clusterv1.Machine{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "default",
		},
	}
	for _, opt := range opts {
		opt(m)
	}
	return m
}
