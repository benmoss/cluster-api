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

	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	kerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apiserver/pkg/storage/names"
	"k8s.io/client-go/tools/record"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1alpha3"
	bootstrapv1 "sigs.k8s.io/cluster-api/bootstrap/kubeadm/api/v1alpha3"
	"sigs.k8s.io/cluster-api/controllers/external"
	controlplanev1 "sigs.k8s.io/cluster-api/controlplane/kubeadm/api/v1alpha3"
	"sigs.k8s.io/cluster-api/controlplane/kubeadm/internal"
	capierrors "sigs.k8s.io/cluster-api/errors"
	"sigs.k8s.io/cluster-api/util"
	"sigs.k8s.io/cluster-api/util/hash"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type ScaleController struct {
	Client            client.Client
	managementCluster internal.ManagementCluster
	recorder          record.EventRecorder
}

func (s *ScaleController) Initialize(ctx context.Context, controlPlane *internal.ControlPlane) (ctrl.Result, error) {
	logger := controlPlane.Logger()

	if err := s.cloneConfigsAndGenerateMachine(ctx, controlPlane); err != nil {
		logger.Error(err, "Failed to create initial control plane Machine")
		s.recorder.Eventf(controlPlane.KCP, corev1.EventTypeWarning, "FailedInitialization", "Failed to create initial control plane Machine for cluster %s/%s control plane: %v", controlPlane.Cluster.Namespace, controlPlane.Cluster.Name, err)
		return ctrl.Result{}, err
	}

	// Requeue the control plane, in case there are additional operations to perform
	return ctrl.Result{Requeue: true}, nil
}

func (s *ScaleController) ScaleUp(ctx context.Context, controlPlane *internal.ControlPlane) (ctrl.Result, error) {
	logger := controlPlane.Logger()

	if err := s.reconcileHealth(ctx, controlPlane); err != nil {
		return ctrl.Result{}, &capierrors.RequeueAfterError{RequeueAfter: healthCheckFailedRequeueAfter}
	}

	// Create the bootstrap configuration
	if err := s.cloneConfigsAndGenerateMachine(ctx, controlPlane); err != nil {
		logger.Error(err, "Failed to create additional control plane Machine")
		s.recorder.Eventf(controlPlane.KCP, corev1.EventTypeWarning, "FailedScaleUp", "Failed to create additional control plane Machine for cluster %s/%s control plane: %v", controlPlane.Cluster.Namespace, controlPlane.Cluster.Name, err)
		return ctrl.Result{}, err
	}

	// Requeue the control plane, in case there are other operations to perform
	return ctrl.Result{Requeue: true}, nil
}

func (s *ScaleController) ScaleDown(ctx context.Context, controlPlane *internal.ControlPlane) (ctrl.Result, error) {
	logger := controlPlane.Logger()

	workloadCluster, err := s.managementCluster.GetWorkloadCluster(ctx, util.ObjectKey(controlPlane.Cluster))
	if err != nil {
		logger.Error(err, "Failed to create client to workload cluster")
		return ctrl.Result{}, errors.Wrapf(err, "failed to create client to workload cluster")
	}

	// Wait for any delete in progress to complete before deleting another Machine
	if controlPlane.HasDeletingMachine() {
		return ctrl.Result{}, &capierrors.RequeueAfterError{RequeueAfter: deleteRequeueAfter}
	}

	if err := s.reconcileHealth(ctx, controlPlane); err != nil {
		return ctrl.Result{}, &capierrors.RequeueAfterError{RequeueAfter: healthCheckFailedRequeueAfter}
	}

	machineToDelete, err := selectMachineForScaleDown(controlPlane)
	if err != nil {
		return ctrl.Result{}, errors.Wrap(err, "failed to select machine for scale down")
	}

	if machineToDelete == nil {
		logger.Info("Failed to pick control plane Machine to delete")
		return ctrl.Result{}, errors.New("failed to pick control plane Machine to delete")
	}

	// If etcd leadership is on machine that is about to be deleted, move it to the newest member available.
	etcdLeaderCandidate := controlPlane.Machines.Newest()
	if err := workloadCluster.ForwardEtcdLeadership(ctx, machineToDelete, etcdLeaderCandidate); err != nil {
		logger.Error(err, "Failed to move leadership to candidate machine", "candidate", etcdLeaderCandidate.Name)
		return ctrl.Result{}, err
	}
	if err := workloadCluster.RemoveEtcdMemberForMachine(ctx, machineToDelete); err != nil {
		logger.Error(err, "Failed to remove etcd member for machine")
		return ctrl.Result{}, err
	}

	if err := s.managementCluster.TargetClusterControlPlaneIsHealthy(ctx, util.ObjectKey(controlPlane.Cluster), controlPlane.KCP.Name); err != nil {
		logger.V(2).Info("Waiting for control plane to pass control plane health check before removing a control plane machine", "cause", err)
		s.recorder.Eventf(controlPlane.KCP, corev1.EventTypeWarning, "ControlPlaneUnhealthy",
			"Waiting for control plane to pass control plane health check before removing a control plane machine: %v", err)
		return ctrl.Result{}, &capierrors.RequeueAfterError{RequeueAfter: healthCheckFailedRequeueAfter}

	}
	if err := workloadCluster.RemoveMachineFromKubeadmConfigMap(ctx, machineToDelete); err != nil {
		logger.Error(err, "Failed to remove machine from kubeadm ConfigMap")
		return ctrl.Result{}, err
	}

	logger = logger.WithValues("machine", machineToDelete)
	if err := s.Client.Delete(ctx, machineToDelete); err != nil && !apierrors.IsNotFound(err) {
		logger.Error(err, "Failed to delete control plane machine")
		s.recorder.Eventf(controlPlane.KCP, corev1.EventTypeWarning, "FailedScaleDown",
			"Failed to delete control plane Machine %s for cluster %s/%s control plane: %v", machineToDelete.Name, controlPlane.Cluster.Namespace, controlPlane.Cluster.Name, err)
		return ctrl.Result{}, err
	}

	// Requeue the control plane, in case there are additional operations to perform
	return ctrl.Result{Requeue: true}, nil
}

func (s *ScaleController) cloneConfigsAndGenerateMachine(ctx context.Context, controlPlane *internal.ControlPlane) error {
	var errs []error

	// Since the cloned resource should eventually have a controller ref for the Machine, we create an
	// OwnerReference here without the Controller field set
	infraCloneOwner := &metav1.OwnerReference{
		APIVersion: controlplanev1.GroupVersion.String(),
		Kind:       "KubeadmControlPlane",
		Name:       controlPlane.KCP.Name,
		UID:        controlPlane.KCP.UID,
	}

	// Clone the infrastructure template
	infraRef, err := external.CloneTemplate(ctx, &external.CloneTemplateInput{
		Client:      s.Client,
		TemplateRef: &controlPlane.KCP.Spec.InfrastructureTemplate,
		Namespace:   controlPlane.KCP.Namespace,
		OwnerRef:    infraCloneOwner,
		ClusterName: controlPlane.Cluster.Name,
		Labels:      internal.ControlPlaneLabelsForClusterWithHash(controlPlane.Cluster.Name, hash.Compute(&controlPlane.KCP.Spec)),
	})
	if err != nil {
		// Safe to return early here since no resources have been created yet.
		return errors.Wrap(err, "failed to clone infrastructure template")
	}

	// Clone the bootstrap configuration
	bootstrapRef, err := s.generateKubeadmConfig(ctx, controlPlane)
	if err != nil {
		errs = append(errs, errors.Wrap(err, "failed to generate bootstrap config"))
	}

	// Only proceed to generating the Machine if we haven't encountered an error
	if len(errs) == 0 {
		if err := s.generateMachine(ctx, controlPlane, infraRef, bootstrapRef); err != nil {
			errs = append(errs, errors.Wrap(err, "failed to create Machine"))
		}
	}

	// If we encountered any errors, attempt to clean up any dangling resources
	if len(errs) > 0 {
		if err := s.cleanupFromGeneration(ctx, infraRef, bootstrapRef); err != nil {
			errs = append(errs, errors.Wrap(err, "failed to cleanup generated resources"))
		}

		return kerrors.NewAggregate(errs)
	}

	return nil
}

func (s *ScaleController) generateKubeadmConfig(ctx context.Context, controlPlane *internal.ControlPlane) (*corev1.ObjectReference, error) {
	// Create an owner reference without a controller reference because the owning controller is the machine controller
	owner := metav1.OwnerReference{
		APIVersion: controlplanev1.GroupVersion.String(),
		Kind:       "KubeadmControlPlane",
		Name:       controlPlane.KCP.Name,
		UID:        controlPlane.KCP.UID,
	}

	bootstrapConfig := &bootstrapv1.KubeadmConfig{
		ObjectMeta: metav1.ObjectMeta{
			Name:            names.SimpleNameGenerator.GenerateName(controlPlane.KCP.Name + "-"),
			Namespace:       controlPlane.KCP.Namespace,
			Labels:          internal.ControlPlaneLabelsForClusterWithHash(controlPlane.Cluster.Name, hash.Compute(&controlPlane.KCP.Spec)),
			OwnerReferences: []metav1.OwnerReference{owner},
		},
		Spec: *controlPlane.JoinControlPlaneConfig(),
	}

	if err := s.Client.Create(ctx, bootstrapConfig); err != nil {
		return nil, errors.Wrap(err, "Failed to create bootstrap configuration")
	}

	bootstrapRef := &corev1.ObjectReference{
		APIVersion: bootstrapv1.GroupVersion.String(),
		Kind:       "KubeadmConfig",
		Name:       bootstrapConfig.GetName(),
		Namespace:  bootstrapConfig.GetNamespace(),
		UID:        bootstrapConfig.GetUID(),
	}

	return bootstrapRef, nil
}

func (s *ScaleController) generateMachine(ctx context.Context, controlPlane *internal.ControlPlane, infraRef, bootstrapRef *corev1.ObjectReference) error {
	machine := &clusterv1.Machine{
		ObjectMeta: metav1.ObjectMeta{
			Name:      names.SimpleNameGenerator.GenerateName(controlPlane.KCP.Name + "-"),
			Namespace: controlPlane.KCP.Namespace,
			Labels:    internal.ControlPlaneLabelsForClusterWithHash(controlPlane.Cluster.Name, hash.Compute(&controlPlane.KCP.Spec)),
			OwnerReferences: []metav1.OwnerReference{
				*metav1.NewControllerRef(controlPlane.KCP, controlplanev1.GroupVersion.WithKind("KubeadmControlPlane")),
			},
		},
		Spec: clusterv1.MachineSpec{
			ClusterName:       controlPlane.Cluster.Name,
			Version:           &controlPlane.KCP.Spec.Version,
			InfrastructureRef: *infraRef,
			Bootstrap: clusterv1.Bootstrap{
				ConfigRef: bootstrapRef,
			},
			FailureDomain: controlPlane.FailureDomainWithFewestMachines(),
		},
	}

	if err := s.Client.Create(ctx, machine); err != nil {
		return errors.Wrap(err, "Failed to create machine")
	}
	return nil
}

func (s *ScaleController) cleanupFromGeneration(ctx context.Context, remoteRefs ...*corev1.ObjectReference) error {
	var errs []error

	for _, ref := range remoteRefs {
		if ref != nil {
			config := &unstructured.Unstructured{}
			config.SetKind(ref.Kind)
			config.SetAPIVersion(ref.APIVersion)
			config.SetNamespace(ref.Namespace)
			config.SetName(ref.Name)

			if err := s.Client.Delete(ctx, config); err != nil && !apierrors.IsNotFound(err) {
				errs = append(errs, errors.Wrap(err, "failed to cleanup generated resources after error"))
			}
		}
	}

	return kerrors.NewAggregate(errs)
}

// reconcileHealth performs health checks for control plane components and etcd
// It removes any etcd members that do not have a corresponding node.
func (s *ScaleController) reconcileHealth(ctx context.Context, controlPlane *internal.ControlPlane) error {
	logger := controlPlane.Logger()

	// Do a health check of the Control Plane components
	if err := s.managementCluster.TargetClusterControlPlaneIsHealthy(ctx, util.ObjectKey(controlPlane.Cluster), controlPlane.KCP.Name); err != nil {
		logger.V(2).Info("Waiting for control plane to pass control plane health check to continue reconciliation", "cause", err)
		s.recorder.Eventf(controlPlane.KCP, corev1.EventTypeWarning, "ControlPlaneUnhealthy",
			"Waiting for control plane to pass control plane health check to continue reconciliation: %v", err)
		return &capierrors.RequeueAfterError{RequeueAfter: healthCheckFailedRequeueAfter}
	}

	// Ensure etcd is healthy
	if err := s.managementCluster.TargetClusterEtcdIsHealthy(ctx, util.ObjectKey(controlPlane.Cluster), controlPlane.KCP.Name); err != nil {
		// If there are any etcd members that do not have corresponding nodes, remove them from etcd and from the kubeadm configmap.
		// This will solve issues related to manual control-plane machine deletion.
		workloadCluster, err := s.managementCluster.GetWorkloadCluster(ctx, util.ObjectKey(controlPlane.Cluster))
		if err != nil {
			return err
		}
		if err := workloadCluster.ReconcileEtcdMembers(ctx); err != nil {
			logger.V(2).Info("Failed attempt to remove potential hanging etcd members to pass etcd health check to continue reconciliation", "cause", err)
		}

		logger.V(2).Info("Waiting for control plane to pass etcd health check to continue reconciliation", "cause", err)
		s.recorder.Eventf(controlPlane.KCP, corev1.EventTypeWarning, "ControlPlaneUnhealthy",
			"Waiting for control plane to pass etcd health check to continue reconciliation: %v", err)
		return &capierrors.RequeueAfterError{RequeueAfter: healthCheckFailedRequeueAfter}
	}

	return nil
}

func selectMachineForScaleDown(controlPlane *internal.ControlPlane) (*clusterv1.Machine, error) {
	machines := controlPlane.Machines
	if needingUpgrade := controlPlane.MachinesNeedingUpgrade(); needingUpgrade.Len() > 0 {
		machines = needingUpgrade
	}
	return controlPlane.MachineInFailureDomainWithMostMachines(machines)
}
