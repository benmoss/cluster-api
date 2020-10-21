package autoscaler

import (
	"testing"

	"github.com/benmoss/autoscaler-tests/test"
)

func TestAutoscaling(t *testing.T) {
	test.ClusterAutoscalerSuite(t, &Provider{})
}
