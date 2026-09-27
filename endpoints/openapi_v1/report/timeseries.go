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

package report

import (
	"net/http"

	"github.com/rainway-ai-gateway/ai-gateway-api/lib/xerror"
	"github.com/rainway-ai-gateway/ai-gateway-api/lib/xreq"
	"github.com/rainway-ai-gateway/ai-gateway-api/model/iauth"
	"github.com/rainway-ai-gateway/ai-gateway-api/model/ireport"
)

// ReportTimeSeriesRoute serves GET /open-api/v1/report/timeseries.
var ReportTimeSeriesRoute = &xreq.Endpoint{
	Path:       "/report/timeseries",
	Method:     http.MethodGet,
	Handler:    xreq.Convert(ReportTimeSeriesAction),
	Authorizer: iauth.FA(iauth.FeatureReport, iauth.ActionReadAll),
}

type timeSeriesResponse struct {
	BucketSec int                    `json:"bucket_sec"`
	Series    []*ireport.MetricPoint `json:"series"`
}

// ReportTimeSeriesAction returns one metric time series; the bucket width
// is computed by the manager from the window.
func ReportTimeSeriesAction(req *http.Request) (interface{}, error) {
	query, err := bindCommonQuery(req)
	if err != nil {
		return nil, err
	}
	base, err := query.toBaseQuery()
	if err != nil {
		return nil, err
	}

	metric := req.Form.Get("metric")
	if metric == "" {
		return nil, xerror.WrapParamErrorWithMsg("%s", "metric is required")
	}
	dimension := req.Form.Get("dimension")

	manager, err := reportManager(req)
	if err != nil {
		return nil, err
	}

	series, err := manager.TimeSeries(req.Context(), &ireport.TimeSeriesQuery{
		BaseQuery: *base,
		Metric:    metric,
		Dimension: dimension,
	})
	if err != nil {
		return nil, err
	}

	return &timeSeriesResponse{
		BucketSec: ireport.BucketSeconds(base.End.Sub(base.Start)),
		Series:    series,
	}, nil
}
