package helpers

import (
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1alpha3"
	controlplanev1 "sigs.k8s.io/cluster-api/controlplane/kubeadm/api/v1alpha3"
	"sigs.k8s.io/cluster-api/util/hash"
)

type MachineOpt func(*clusterv1.Machine)

func Machine(name string, opts ...MachineOpt) *clusterv1.Machine {
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

func WithFailureDomain(fd string) MachineOpt {
	return func(m *clusterv1.Machine) {
		m.Spec.FailureDomain = &fd
	}
}

func WithTimestamp(t time.Time) MachineOpt {
	return func(m *clusterv1.Machine) {
		m.CreationTimestamp = metav1.NewTime(t)
	}
}

func WithValidHash(kcp controlplanev1.KubeadmControlPlaneSpec) MachineOpt {
	return func(m *clusterv1.Machine) {
		WithHash(hash.Compute(&kcp))(m)
	}
}

func WithHash(hash string) MachineOpt {
	return func(m *clusterv1.Machine) {
		m.SetLabels(map[string]string{controlplanev1.KubeadmControlPlaneHashLabelKey: hash})
	}
}

func WithAnnotation(key, value string) MachineOpt {
	return func(m *clusterv1.Machine) {
		m.SetAnnotations(map[string]string{key: value})
	}
}
