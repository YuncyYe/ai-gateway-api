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

package ai_context

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

type fakeAIContextRuleStorager struct {
	fetchAllFn   func(ctx context.Context) ([]*ContextRuleRow, error)
	replaceAllFn func(ctx context.Context, rules []*ContextRuleRow) error

	fetchAllCalls   int
	replaceAllCalls [][]*ContextRuleRow
}

func (s *fakeAIContextRuleStorager) FetchAll(ctx context.Context) ([]*ContextRuleRow, error) {
	s.fetchAllCalls++
	if s.fetchAllFn != nil {
		return s.fetchAllFn(ctx)
	}
	return nil, nil
}

func (s *fakeAIContextRuleStorager) ReplaceAll(ctx context.Context, rules []*ContextRuleRow) error {
	s.replaceAllCalls = append(s.replaceAllCalls, rules)
	if s.replaceAllFn != nil {
		return s.replaceAllFn(ctx, rules)
	}
	return nil
}

var _ AIContextStorager = (*fakeAIContextRuleStorager)(nil)

type fakeAIContextSettingsStorager struct {
	getFn    func(ctx context.Context) (*SettingsRow, error)
	upsertFn func(ctx context.Context, row *SettingsRow) error

	getCalls    int
	upsertCalls []*SettingsRow
}

func (s *fakeAIContextSettingsStorager) Get(ctx context.Context) (*SettingsRow, error) {
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

func (s *fakeAIContextSettingsStorager) Upsert(ctx context.Context, row *SettingsRow) error {
	s.upsertCalls = append(s.upsertCalls, row)
	if s.upsertFn != nil {
		return s.upsertFn(ctx, row)
	}
	return nil
}

var _ AIContextSettingsStorager = (*fakeAIContextSettingsStorager)(nil)

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
