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
//limitations under the License. All rights reserved.

package keyrotate

// SweepTable describes one sensitive column to scan.
type SweepTable struct {
	Name   string
	PKCol  string
	ValCol string
}

// RowRef is one scanned row (primary key + sensitive value).
type RowRef struct {
	PK    interface{}
	Value string
}

var sweepTables = []SweepTable{
	{Name: "providers", PKCol: "name", ValCol: "api_keys"},
	{Name: "api_keys", PKCol: "inner_id", ValCol: "api_key"},
}

// TablesForScope resolves the scope to the concrete table list.
func TablesForScope(scope Scope) []SweepTable {
	if scope == ScopeProviders {
		return sweepTables[:1]
	}
	if scope == ScopeAPIKeys {
		return sweepTables[1:]
	}
	return sweepTables
}
