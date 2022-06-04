//go:build integ
// +build integ

// Copyright Istio Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package networking

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/util/rand"

	"istio.io/istio/pkg/test"
	"istio.io/istio/pkg/test/framework"
	"istio.io/istio/pkg/test/framework/components/echo"
	"istio.io/istio/pkg/test/framework/components/echo/common/deployment"
	"istio.io/istio/pkg/test/framework/components/istio"
	"istio.io/istio/pkg/test/framework/resource"
	"istio.io/istio/pkg/test/util/retry"
)

var (
	ist  istio.Instance
	apps = deployment.SingleNamespaceView{}
)

func TestMain(m *testing.M) {
	framework.
		NewSuite(m).
		Setup(istio.Setup(&ist, nil)).
		Setup(deployment.SetupSingleNamespace(&apps, deployment.Config{})).
		Run()
}

const ReqWithoutQueryFormat = "%REQ_WITHOUT_QUERY(:PATH)%"

func TestReqWithoutQueryAccessLogs(t *testing.T) {
	framework.NewTest(t).
		Features("observability.telemetry.logging").
		Run(func(t framework.TestContext) {
			t.NewSubTest("meshConfigReqWithoutQueryText").Run(func(t framework.TestContext) {
				istio.PatchMeshConfigOrFail(t, ist.Settings().SystemNamespace, apps.A.Clusters(), fmt.Sprintf(`
				accessLogFile: "/dev/stdout"
				accessLogFormat: "%s\n"
				accessLogEncoding: TEXT
				`, ReqWithoutQueryFormat))
				runAccessLogsTests(t)
			})
			t.NewSubTest("meshConfigReqWithoutQueryJSON").Run(func(t framework.TestContext) {
				istio.PatchMeshConfigOrFail(t, ist.Settings().SystemNamespace, apps.A.Clusters(), fmt.Sprintf(`
				accessLogFile: "/dev/stdout"
				accessLogFormat: '{"path": "%s"}'
				accessLogEncoding: JSON
				`, ReqWithoutQueryFormat))
				runAccessLogsTests(t)
			})
			t.NewSubTest("extensionProviderReqWithoutQueryText").Run(func(t framework.TestContext) {
				istio.PatchMeshConfigOrFail(t, ist.Settings().SystemNamespace, apps.A.Clusters(), fmt.Sprintf(`
				extensionProviders:
				-	name: envoy-custom-format
					envoyFileAccessLog:
					logFormat:
						text: "%s"
				defaultProviders:
					accessLogging:
					- 	envoy-custom-format
				`, ReqWithoutQueryFormat))
				runAccessLogsTests(t)
			})
			t.NewSubTest("extensionProviderReqWithoutQueryJSON").Run(func(t framework.TestContext) {
				istio.PatchMeshConfigOrFail(t, ist.Settings().SystemNamespace, apps.A.Clusters(), fmt.Sprintf(`
				extensionProviders:
				-	name: envoy-custom-format
					envoyFileAccessLog:
					logFormat:
						labels:
							path: "%s"
				defaultProviders:
					accessLogging:
					- 	envoy-custom-format
				`, ReqWithoutQueryFormat))
				runAccessLogsTests(t)
			})
		})
}

func setupConfig(meshConfig string) func(resource.Context, *istio.Config) {
	return func(ctx resource.Context, cfg *istio.Config) {
		cfg.ControlPlaneValues = meshConfig
	}
}

func runAccessLogsTests(t framework.TestContext) {
	testID := rand.String(16)
	to := apps.B

	// Validate that we always get logs when %REQ_WITHOUT_QUERY% is specified since it requires a formatter. See: https://github.com/istio/istio/issues/39271
	retry.UntilSuccessOrFail(t, func() error {
		apps.A[0].CallOrFail(t, echo.CallOptions{
			To: to,
			Port: echo.Port{
				Name: "http",
			},
			HTTP: echo.HTTP{
				Path: "/" + testID,
			},
		})
		// Retry a bit to get the logs. There is some delay before they are output, so they may not be immediately ready
		// If not ready in 5s, we retry sending a call again.
		retry.UntilSuccessOrFail(t, func() error {
			count := logCount(t, to, testID)
			if count == 0 {
				return fmt.Errorf("expected logs, got none")
			}
			return nil
		}, retry.Timeout(time.Second*5))
		return nil
	})
}

func logCount(t test.Failer, to echo.Target, testID string) float64 {
	counts := map[string]float64{}
	for _, w := range to.WorkloadsOrFail(t) {
		var logs string
		l, err := w.Sidecar().Logs()
		if err != nil {
			t.Fatalf("failed getting logs: %v", err)
		}
		logs += l
		if c := float64(strings.Count(logs, testID)); c > 0 {
			counts[w.Cluster().Name()] = c
		}
	}
	var total float64
	for _, c := range counts {
		total += c
	}
	return total
}
