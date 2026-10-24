// Copyright(c) 2026 The Rainway AI Gateway (壬远AI网关) Authors.
//
//Licensed under the Apache License, Version 2.0 (the "License");
//you may not use this file except in compliance with the License.
//You may obtain a copy of the License at
//
//http://www.apache.org/licenses/LICENSE-2.0
//
//Unless required by applicable law or agreed to in writing, software
//distributed under the License is distributed on an "AS IS" BASIS,
//WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
//See the License for the specific language governing permissions and
//limitations under the License.

package openapi_v1

import (
	"testing"

	"github.com/gorilla/mux"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rainway-ai-gateway/ai-gateway-api/model/ireport"
	"github.com/rainway-ai-gateway/ai-gateway-api/stateful/container"
)

type stubReportManager struct {
	ireport.ReportManagerInterface
}

func TestEndpoints(t *testing.T) {
	eps := endpoints()
	require.NotEmpty(t, eps)

	paths := make(map[string]bool)
	for _, ep := range eps {
		require.NotNil(t, ep)
		assert.NotEmpty(t, ep.Path)
		assert.NotEmpty(t, ep.Method)
		paths[ep.Path] = true
	}

	// Sanity check that some well-known paths are registered.
	assert.Contains(t, paths, "/auth/users")
	assert.Contains(t, paths, "/ai-cache-rules")
	assert.Contains(t, paths, "/traffic-mirror-rules")
	assert.Contains(t, paths, "/intent-config")
	assert.Contains(t, paths, "/clusters")
	assert.Contains(t, paths, "/epp-pool")
	assert.Contains(t, paths, "/epp-assignments")
	assert.Contains(t, paths, "/epp-assignments/{cluster}")
	assert.Contains(t, paths, "/model-prices")
	assert.Contains(t, paths, "/model-prices/{id}")
	assert.Contains(t, paths, "/model-prices/import")
	assert.Contains(t, paths, "/operation-logs")
}

func TestRegisterEndpoints(t *testing.T) {
	root := mux.NewRouter()
	sub := RegisterEndpoints(root)

	require.NotNil(t, sub)

	var matchedPaths []string
	err := sub.Walk(func(route *mux.Route, router *mux.Router, ancestors []*mux.Route) error {
		path, err := route.GetPathTemplate()
		if err == nil {
			matchedPaths = append(matchedPaths, path)
		}
		return nil
	})
	require.NoError(t, err)
	assert.NotEmpty(t, matchedPaths)
}

func TestEndpoints_ReportNotAssembled(t *testing.T) {
	container.ReportManager = nil

	found := false
	for _, ep := range endpoints() {
		if len(ep.Path) >= len("/report/") && ep.Path[:len("/report/")] == "/report/" {
			found = true
			break
		}
	}
	assert.False(t, found, "report endpoints must not be registered without the report module")
}

func TestEndpoints_ReportAssembled(t *testing.T) {
	container.ReportManager = &stubReportManager{}
	defer func() { container.ReportManager = nil }()

	var reportPaths []string
	for _, ep := range endpoints() {
		if len(ep.Path) >= len("/report/") && ep.Path[:len("/report/")] == "/report/" {
			reportPaths = append(reportPaths, ep.Path)
		}
	}
	require.Len(t, reportPaths, 5)
}
