package autoscaler

import (
	"fmt"
	"os"

	"k8s.io/client-go/discovery"
	"k8s.io/kubernetes/test/e2e/framework"
)

const (
	defaultCAPIGroup              = "cluster.x-k8s.io"
	CAPIGroupEnvVar               = "CAPI_GROUP"
	resourceNameMachineDeployment = "machinedeployments"
)

// getCAPIGroup returns a string that specifies the group for the API.
// It will return either the value from the
// CAPI_GROUP environment variable, or the default value i.e cluster.x-k8s.io.
func getCAPIGroup() string {
	g := os.Getenv(CAPIGroupEnvVar)
	if g == "" {
		g = defaultCAPIGroup
	}
	framework.Logf("Using API Group %q", g)
	return g
}

func getAPIGroupPreferredVersion(client discovery.DiscoveryInterface, APIGroup string) (string, error) {
	groupList, err := client.ServerGroups()
	if err != nil {
		return "", fmt.Errorf("failed to get ServerGroups: %v", err)
	}

	for _, group := range groupList.Groups {
		if group.Name == APIGroup {
			return group.PreferredVersion.Version, nil
		}
	}

	return "", fmt.Errorf("failed to find API group %q", APIGroup)
}
