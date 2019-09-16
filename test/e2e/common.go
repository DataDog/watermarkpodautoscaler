package e2e

import (
	"testing"
	"time"

	"github.com/DataDog/watermarkpodautoscaler/test/e2e/utils"

	framework "github.com/operator-framework/operator-sdk/pkg/test"
	"github.com/operator-framework/operator-sdk/pkg/test/e2eutil"
)

func initTestFwkResources(t *testing.T, deploymentName string) (string, *framework.TestCtx, *framework.Framework) {
	ctx := framework.NewTestCtx(t)

	t.Log("Initialized cluster resources")
	namespace, err := ctx.GetNamespace()
	if err != nil {
		t.Fatal(err)
	}
	err = utils.GenerateClusterRoleManifest(t, ctx, namespace, ctx.GetID(), deployDirPath, utils.GenerateClusterRoleManifestOptions{
		CleanupTimeout:       cleanupTimeout,
		CleanupRetryInterval: cleanupRetryInterval,
	})
	if err != nil {
		t.Fatal(err)
	}

	err = ctx.InitializeClusterResources(&framework.CleanupOptions{TestContext: ctx, Timeout: cleanupTimeout, RetryInterval: cleanupRetryInterval})
	if err != nil {
		t.Fatalf("failed to initialize cluster resources: %v", err)
	}

	// get global framework variables
	f := framework.Global
	err = e2eutil.WaitForOperatorDeployment(t, f.KubeClient, namespace, deploymentName, 1, retryInterval, timeout)
	if err != nil {
		t.Fatal(err)
	}
	return namespace, ctx, f
}

var (
	retryInterval        = time.Second * 5
	timeout              = time.Second * 60
	cleanupRetryInterval = time.Second * 10
	cleanupTimeout       = time.Second * 240
)

const (
	deployDirPath = "deploy"
)
