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

	"github.com/blang/semver"
	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1alpha3"
	controlplanev1 "sigs.k8s.io/cluster-api/controlplane/kubeadm/api/v1alpha3"
	"sigs.k8s.io/cluster-api/controlplane/kubeadm/internal"
	capierrors "sigs.k8s.io/cluster-api/errors"
	"sigs.k8s.io/cluster-api/util"
	ctrl "sigs.k8s.io/controller-runtime"
)

func (r *KubeadmControlPlaneReconciler) initializeControlPlane(
	ctx context.Context,
	cluster *clusterv1.Cluster,
	kcp *controlplanev1.KubeadmControlPlane,
	controlPlane *internal.ControlPlane,
) (ctrl.Result, error) {
	logger := controlPlane.Logger()

	bootstrapSpec := controlPlane.InitialControlPlaneConfig()
	fd := controlPlane.FailureDomainWithFewestMachines()
	if err := r.cloneConfigsAndGenerateMachine(ctx, cluster, kcp, bootstrapSpec, fd); err != nil {
		logger.Error(err, "Failed to create initial control plane Machine")
		r.recorder.Eventf(kcp, corev1.EventTypeWarning, "FailedInitialization", "Failed to create initial control plane Machine for cluster %s/%s control plane: %v", cluster.Namespace, cluster.Name, err)
		return ctrl.Result{}, err
	}

	// Requeue the control plane, in case there are additional operations to perform
	return ctrl.Result{Requeue: true}, nil
}

func (r *KubeadmControlPlaneReconciler) scaleUpControlPlane(
	ctx context.Context,
	cluster *clusterv1.Cluster,
	kcp *controlplanev1.KubeadmControlPlane,
	controlPlane *internal.ControlPlane,
) (ctrl.Result, error) {
	logger := controlPlane.Logger()

	// TODO: handle reconciliation of etcd members and kubeadm config in case they get out of sync with cluster

	workloadCluster, err := r.managementCluster.GetWorkloadCluster(ctx, util.ObjectKey(cluster))
	if err != nil {
		logger.Error(err, "failed to get remote client for workload cluster", "cluster key", util.ObjectKey(cluster))
		return ctrl.Result{}, err
	}

	parsedVersion, err := semver.ParseTolerant(kcp.Spec.Version)
	if err != nil {
		return ctrl.Result{}, errors.Wrapf(err, "failed to parse kubernetes version %q", kcp.Spec.Version)
	}

	if err := workloadCluster.ReconcileKubeletRBACRole(ctx, parsedVersion); err != nil {
		return ctrl.Result{}, errors.Wrap(err, "failed to reconcile the remote kubelet RBAC role")
	}

	if err := workloadCluster.ReconcileKubeletRBACBinding(ctx, parsedVersion); err != nil {
		return ctrl.Result{}, errors.Wrap(err, "failed to reconcile the remote kubelet RBAC binding")
	}

	if err := workloadCluster.UpdateKubernetesVersionInKubeadmConfigMap(ctx, parsedVersion); err != nil {
		return ctrl.Result{}, errors.Wrap(err, "failed to update the kubernetes version in the kubeadm config map")
	}

	if kcp.Spec.KubeadmConfigSpec.ClusterConfiguration != nil {
		imageRepository := kcp.Spec.KubeadmConfigSpec.ClusterConfiguration.ImageRepository
		if err := workloadCluster.UpdateImageRepositoryInKubeadmConfigMap(ctx, imageRepository); err != nil {
			return ctrl.Result{}, errors.Wrap(err, "failed to update the image repository in the kubeadm config map")
		}
	}

	if kcp.Spec.KubeadmConfigSpec.ClusterConfiguration != nil && kcp.Spec.KubeadmConfigSpec.ClusterConfiguration.Etcd.Local != nil {
		meta := kcp.Spec.KubeadmConfigSpec.ClusterConfiguration.Etcd.Local.ImageMeta
		if err := workloadCluster.UpdateEtcdVersionInKubeadmConfigMap(ctx, meta.ImageRepository, meta.ImageTag); err != nil {
			return ctrl.Result{}, errors.Wrap(err, "failed to update the etcd version in the kubeadm config map")
		}
	}

	if err := workloadCluster.UpdateKubeletConfigMap(ctx, parsedVersion); err != nil {
		return ctrl.Result{}, errors.Wrap(err, "failed to upgrade kubelet config map")
	}

	if err := r.managementCluster.TargetClusterControlPlaneIsHealthy(ctx, util.ObjectKey(cluster), kcp.Name); err != nil {
		logger.V(2).Info("Waiting for control plane to pass control plane health check before adding an additional control plane machine", "cause", err)
		r.recorder.Eventf(kcp, corev1.EventTypeWarning, "ControlPlaneUnhealthy", "Waiting for control plane to pass control plane health check before adding additional control plane machine: %v", err)
		return ctrl.Result{}, &capierrors.RequeueAfterError{RequeueAfter: healthCheckFailedRequeueAfter}
	}

	if err := r.managementCluster.TargetClusterEtcdIsHealthy(ctx, util.ObjectKey(cluster), kcp.Name); err != nil {
		logger.V(2).Info("Waiting for control plane to pass etcd health check before adding an additional control plane machine", "cause", err)
		r.recorder.Eventf(kcp, corev1.EventTypeWarning, "ControlPlaneUnhealthy", "Waiting for control plane to pass etcd health check before adding additional control plane machine: %v", err)
		return ctrl.Result{}, &capierrors.RequeueAfterError{RequeueAfter: healthCheckFailedRequeueAfter}
	}

	// Create the bootstrap configuration
	bootstrapSpec := controlPlane.JoinControlPlaneConfig()
	fd := controlPlane.FailureDomainWithFewestMachines()
	if err := r.cloneConfigsAndGenerateMachine(ctx, cluster, kcp, bootstrapSpec, fd); err != nil {
		logger.Error(err, "Failed to create additional control plane Machine")
		r.recorder.Eventf(kcp, corev1.EventTypeWarning, "FailedScaleUp", "Failed to create additional control plane Machine for cluster %s/%s control plane: %v", cluster.Namespace, cluster.Name, err)
		return ctrl.Result{}, err
	}

	// Requeue the control plane, in case there are other operations to perform
	return ctrl.Result{Requeue: true}, nil
}

func (r *KubeadmControlPlaneReconciler) scaleDownControlPlane(
	ctx context.Context,
	cluster *clusterv1.Cluster,
	kcp *controlplanev1.KubeadmControlPlane,
	controlPlane *internal.ControlPlane,
) (ctrl.Result, error) {
	logger := controlPlane.Logger()

	workloadCluster, err := r.managementCluster.GetWorkloadCluster(ctx, util.ObjectKey(cluster))
	if err != nil {
		logger.Error(err, "Failed to create client to workload cluster")
		return ctrl.Result{}, errors.Wrapf(err, "failed to create client to workload cluster")
	}

	// We don't want to health check at the beginning of this method to avoid blocking re-entrancy

	// Wait for any delete in progress to complete before deleting another Machine
	if controlPlane.HasDeletingMachine() {
		return ctrl.Result{}, &capierrors.RequeueAfterError{RequeueAfter: deleteRequeueAfter}
	}

	machineToDelete := controlPlane.ScaleStrategy().NextForScaleDown()
	if machineToDelete == nil {
		logger.Info("Failed to pick control plane Machine to delete")
		return ctrl.Result{}, errors.New("failed to pick control plane Machine to delete")
	}

	// Ensure etcd is healthy prior to attempting to remove the member
	if err := r.managementCluster.TargetClusterEtcdIsHealthy(ctx, util.ObjectKey(cluster), kcp.Name); err != nil {
		logger.V(2).Info("Waiting for control plane to pass etcd health check before removing a control plane machine", "cause", err)
		r.recorder.Eventf(kcp, corev1.EventTypeWarning, "ControlPlaneUnhealthy",
			"Waiting for control plane to pass etcd health check before removing a control plane machine: %v", err)
		return ctrl.Result{}, &capierrors.RequeueAfterError{RequeueAfter: healthCheckFailedRequeueAfter}
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

	if err := workloadCluster.RemoveMachineFromKubeadmConfigMap(ctx, machineToDelete); err != nil {
		logger.Error(err, "Failed to remove machine from kubeadm ConfigMap")
		return ctrl.Result{}, err
	}

	// Do a final health check of the Control Plane components prior to actually deleting the machine
	if err := r.managementCluster.TargetClusterControlPlaneIsHealthy(ctx, util.ObjectKey(cluster), kcp.Name); err != nil {
		logger.V(2).Info("Waiting for control plane to pass control plane health check before removing a control plane machine", "cause", err)
		r.recorder.Eventf(kcp, corev1.EventTypeWarning, "ControlPlaneUnhealthy",
			"Waiting for control plane to pass control plane health check before removing a control plane machine: %v", err)
		return ctrl.Result{}, &capierrors.RequeueAfterError{RequeueAfter: healthCheckFailedRequeueAfter}
	}
	logger = logger.WithValues("machine", machineToDelete)
	if err := r.Client.Delete(ctx, machineToDelete); err != nil && !apierrors.IsNotFound(err) {
		logger.Error(err, "Failed to delete control plane machine")
		r.recorder.Eventf(kcp, corev1.EventTypeWarning, "FailedScaleDown",
			"Failed to delete control plane Machine %s for cluster %s/%s control plane: %v", machineToDelete.Name, cluster.Namespace, cluster.Name, err)
		return ctrl.Result{}, err
	}

	// Requeue the control plane, in case there are additional operations to perform
	return ctrl.Result{Requeue: true}, nil
}
