// Copyright(c) 2026 The Rainway AI Gateway (壬远AI网关) Authors.
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

package ai_cache

import (
	"context"

	"github.com/rainway-ai-gateway/ai-gateway-api/model/ioperlog"
	"github.com/rainway-ai-gateway/ai-gateway-api/model/itxn"
	"github.com/rainway-ai-gateway/ai-gateway-api/model/iversion_control"
)

type fakeTxn struct{}

func (f *fakeTxn) AtomExecute(ctx context.Context, do func(context.Context) error) error {
	return do(ctx)
}

var _ itxn.TxnStorager = (*fakeTxn)(nil)

type fakeAICacheRuleStorager struct {
	fetchAllFn   func(ctx context.Context) ([]*AICacheRuleParam, error)
	replaceAllFn func(ctx context.Context, rules []*AICacheRuleParam) error

	fetchAllCalls   int
	replaceAllCalls [][]*AICacheRuleParam
}

func (s *fakeAICacheRuleStorager) FetchAll(ctx context.Context) ([]*AICacheRuleParam, error) {
	s.fetchAllCalls++
	if s.fetchAllFn != nil {
		return s.fetchAllFn(ctx)
	}
	return nil, nil
}

func (s *fakeAICacheRuleStorager) ReplaceAll(ctx context.Context, rules []*AICacheRuleParam) error {
	s.replaceAllCalls = append(s.replaceAllCalls, rules)
	if s.replaceAllFn != nil {
		return s.replaceAllFn(ctx, rules)
	}
	return nil
}

var _ AICacheStorager = (*fakeAICacheRuleStorager)(nil)

type fakeAICacheSemanticSettingsStorager struct {
	getFn    func(ctx context.Context) (*SemanticSettingsRow, error)
	upsertFn func(ctx context.Context, row *SemanticSettingsRow) error

	getCalls    int
	upsertCalls []*SemanticSettingsRow
}

func (s *fakeAICacheSemanticSettingsStorager) Get(ctx context.Context) (*SemanticSettingsRow, error) {
	s.getCalls++
	if s.getFn != nil {
		return s.getFn(ctx)
	}
	// Emulate the singleton table: the last upserted row is what Get reads
	// back (nil while the table is still empty).
	if len(s.upsertCalls) > 0 {
		return s.upsertCalls[len(s.upsertCalls)-1], nil
	}
	return nil, nil
}

func (s *fakeAICacheSemanticSettingsStorager) Upsert(ctx context.Context, row *SemanticSettingsRow) error {
	s.upsertCalls = append(s.upsertCalls, row)
	if s.upsertFn != nil {
		return s.upsertFn(ctx, row)
	}
	return nil
}

var _ AICacheSemanticSettingsStorager = (*fakeAICacheSemanticSettingsStorager)(nil)

type fakeVersionControlStorager struct {
	upsertFn func(ctx context.Context, css *iversion_control.ExportData) (string, error)
}

func (s *fakeVersionControlStorager) UpsertConfigLastExportedVersion(ctx context.Context, css *iversion_control.ExportData) (string, error) {
	if s.upsertFn != nil {
		return s.upsertFn(ctx, css)
	}
	return "", nil
}

var _ iversion_control.VersionControlStorager = (*fakeVersionControlStorager)(nil)

type fakeOperationLogRecorder struct {
	entries []*ioperlog.OperationLogEntry
}

func (f *fakeOperationLogRecorder) Record(ctx context.Context, entry *ioperlog.OperationLogEntry) {
	f.entries = append(f.entries, entry)
}

var _ ioperlog.OperationLogRecorder = (*fakeOperationLogRecorder)(nil)
