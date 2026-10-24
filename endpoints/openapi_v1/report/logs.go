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
	"strconv"

	"github.com/rainway-ai-gateway/ai-gateway-api/lib/xerror"
	"github.com/rainway-ai-gateway/ai-gateway-api/lib/xreq"
	"github.com/rainway-ai-gateway/ai-gateway-api/model/iauth"
	"github.com/rainway-ai-gateway/ai-gateway-api/model/ireport"
)

// ReportLogsRoute serves GET /open-api/v1/report/logs.
var ReportLogsRoute = &xreq.Endpoint{
	Path:       "/report/logs",
	Method:     http.MethodGet,
	Handler:    xreq.Convert(ReportLogsAction),
	Authorizer: iauth.FA(iauth.FeatureReport, iauth.ActionReadAll),
}

// ReportLogsAction returns one page of request log details, newest first.
func ReportLogsAction(req *http.Request) (interface{}, error) {
	query, err := bindCommonQuery(req)
	if err != nil {
		return nil, err
	}
	base, err := query.toBaseQuery()
	if err != nil {
		return nil, err
	}

	logsQuery := &ireport.LogsQuery{BaseQuery: *base}
	if raw := req.Form.Get("requested_models"); raw != "" {
		logsQuery.RequestedModels = splitList(raw)
	}
	if err := bindLogsFilterParams(req, logsQuery); err != nil {
		return nil, err
	}
	if raw := req.Form.Get("err_only"); raw != "" {
		value, err := parseBoolParam(raw, "err_only")
		if err != nil {
			return nil, err
		}
		logsQuery.ErrOnly = *value
	}
	if raw := req.Form.Get("keyword"); raw != "" {
		logsQuery.Keyword = raw
	}
	if raw := req.Form.Get("page"); raw != "" {
		value, err := parsePositiveInt(raw, "page")
		if err != nil {
			return nil, err
		}
		logsQuery.Page = *value
	}
	if raw := req.Form.Get("page_size"); raw != "" {
		value, err := parsePositiveInt(raw, "page_size")
		if err != nil {
			return nil, err
		}
		logsQuery.PageSize = *value
	}

	manager, err := reportManager(req)
	if err != nil {
		return nil, err
	}

	return manager.Logs(req.Context(), logsQuery)
}

// parseBoolParam parses an optional boolean parameter.
func parseBoolParam(raw, name string) (*bool, error) {
	value, err := strconv.ParseBool(raw)
	if err != nil {
		return nil, xerror.WrapParamErrorWithMsg("invalid %s: %s", name, raw)
	}
	return &value, nil
}
