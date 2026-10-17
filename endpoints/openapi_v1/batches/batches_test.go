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

package batches

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rainway-ai-gateway/ai-gateway-api/lib/xerror"
	"github.com/rainway-ai-gateway/ai-gateway-api/model/ibasic"
	"github.com/rainway-ai-gateway/ai-gateway-api/model/ibatch"
	"github.com/rainway-ai-gateway/ai-gateway-api/model/ioperlog"
	"github.com/rainway-ai-gateway/ai-gateway-api/stateful"
	"github.com/rainway-ai-gateway/ai-gateway-api/stateful/container"
)

// fakeBatchManager 手写 callback mock of ibatch.BatchManager。
type fakeBatchManager struct {
	fetchTaskFn func(ctx context.Context, batchID, productName string, allowRedisFallback bool) (*ibatch.BatchTask, error)
	fetchFileFn func(ctx context.Context, fileID, provider, productName string, allowRedisFallback bool) (*ibatch.BatchFile, error)
	listTasksFn func(ctx context.Context, filter *ibatch.BatchTaskFilter) (*ibatch.BatchTaskListResult, error)
	cancelFn    func(ctx context.Context, batchID, productName string) (*ibatch.BatchTask, error)
}

func (f *fakeBatchManager) FetchTask(ctx context.Context, batchID, productName string, allowRedisFallback bool) (*ibatch.BatchTask, error) {
	if f.fetchTaskFn != nil {
		return f.fetchTaskFn(ctx, batchID, productName, allowRedisFallback)
	}
	return nil, nil
}

func (f *fakeBatchManager) FetchFile(ctx context.Context, fileID, provider, productName string, allowRedisFallback bool) (*ibatch.BatchFile, error) {
	if f.fetchFileFn != nil {
		return f.fetchFileFn(ctx, fileID, provider, productName, allowRedisFallback)
	}
	return nil, nil
}

func (f *fakeBatchManager) ListTasks(ctx context.Context, filter *ibatch.BatchTaskFilter) (*ibatch.BatchTaskListResult, error) {
	if f.listTasksFn != nil {
		return f.listTasksFn(ctx, filter)
	}
	return nil, nil
}

func (f *fakeBatchManager) Cancel(ctx context.Context, batchID, productName string) (*ibatch.BatchTask, error) {
	if f.cancelFn != nil {
		return f.cancelFn(ctx, batchID, productName)
	}
	return nil, nil
}

func (f *fakeBatchManager) SetOperationLogManager(manager ioperlog.OperationLogRecorder) {}

var _ ibatch.BatchManager = (*fakeBatchManager)(nil)

func setupBatchManager(m ibatch.BatchManager) func() {
	old := container.BatchManager
	container.BatchManager = m
	return func() { container.BatchManager = old }
}

// newProductRequest 构造带产品线上下文的请求（McProductProbe 中间件
// 在生产链路注入，单测直接写 ctx）。
func newProductRequest(t *testing.T, method, url string) *http.Request {
	t.Helper()
	req := httptest.NewRequest(method, url, nil)
	return req.WithContext(ibasic.NewProductContext(req.Context(), &ibasic.Product{Name: "p1"}))
}

func withPathVars(req *http.Request, vars map[string]string) *http.Request {
	return mux.SetURLVars(req, vars)
}

func TestBatchTaskListAction(t *testing.T) {
	t.Run("过滤与分页参数映射", func(t *testing.T) {
		var gotFilter *ibatch.BatchTaskFilter
		m := &fakeBatchManager{
			listTasksFn: func(ctx context.Context, filter *ibatch.BatchTaskFilter) (*ibatch.BatchTaskListResult, error) {
				gotFilter = filter
				return &ibatch.BatchTaskListResult{
					Tasks:      []*ibatch.BatchTask{{ID: 1, BatchID: "b-1", Status: "completed"}},
					NextCursor: 1,
				}, nil
			},
		}
		defer setupBatchManager(m)()

		req := newProductRequest(t, http.MethodGet,
			"/batches?api_key_id=ak-1&entity_id=e-1&provider=openai&status=completed&start_time=1725091100&end_time=1725091300&cursor=42&limit=10")
		data, err := BatchTaskListAction(req)
		require.NoError(t, err)

		resp, ok := data.(*batchTaskListResponse)
		require.True(t, ok)
		assert.Equal(t, 1, len(resp.List))
		assert.Equal(t, int64(1), resp.NextCursor)

		require.NotNil(t, gotFilter)
		assert.Equal(t, "p1", *gotFilter.ProductName, "product_name 强制取自中间件注入")
		assert.Equal(t, "ak-1", *gotFilter.APIKeyID)
		assert.Equal(t, "e-1", *gotFilter.EntityID)
		assert.Equal(t, "openai", *gotFilter.Provider)
		assert.Equal(t, "completed", *gotFilter.Status)
		assert.Equal(t, int64(42), *gotFilter.CursorID)
		assert.Equal(t, 10, gotFilter.Limit)
		require.NotNil(t, gotFilter.CreatedStart)
		assert.Equal(t, int64(1725091100), gotFilter.CreatedStart.Unix())
		require.NotNil(t, gotFilter.CreatedEnd)
		assert.Equal(t, int64(1725091300), gotFilter.CreatedEnd.Unix())
	})

	t.Run("空结果返回空数组而非 null", func(t *testing.T) {
		m := &fakeBatchManager{
			listTasksFn: func(ctx context.Context, filter *ibatch.BatchTaskFilter) (*ibatch.BatchTaskListResult, error) {
				return &ibatch.BatchTaskListResult{}, nil
			},
		}
		defer setupBatchManager(m)()

		req := newProductRequest(t, http.MethodGet, "/batches")
		data, err := BatchTaskListAction(req)
		require.NoError(t, err)
		resp := data.(*batchTaskListResponse)
		assert.NotNil(t, resp.List)
		assert.Equal(t, 0, len(resp.List))
	})

	t.Run("负 cursor 参数错误", func(t *testing.T) {
		defer setupBatchManager(&fakeBatchManager{})()
		req := newProductRequest(t, http.MethodGet, "/batches?cursor=-1")
		_, err := BatchTaskListAction(req)
		require.Error(t, err)
		assert.Equal(t, 422, xerror.Resolve(err).ErrNo)
	})

	t.Run("缺少 product 上下文时回退默认产品线", func(t *testing.T) {
		defer setupBatchManager(&fakeBatchManager{
			listTasksFn: func(ctx context.Context, filter *ibatch.BatchTaskFilter) (*ibatch.BatchTaskListResult, error) {
				require.NotNil(t, filter.ProductName)
				assert.Equal(t, "p-default", *filter.ProductName, "product_name 应回退为配置默认产品线")
				return &ibatch.BatchTaskListResult{}, nil
			},
		})()
		defer setDefaultProductFallback("p-default")()

		req := httptest.NewRequest(http.MethodGet, "/batches", nil)
		_, err := BatchTaskListAction(req)
		require.NoError(t, err)
	})
}

// fakeProductStorager / fakeProductTxn / setDefaultProductFallback 支撑
// resolveProduct 默认产品线回退路径的单测（对齐 middleware 包
// product_probe_test 的手写 mock 惯例）。
type fakeProductStorager struct {
	fetchProductsFn func(ctx context.Context, param *ibasic.ProductFilter) ([]*ibasic.Product, error)
}

func (f *fakeProductStorager) FetchProducts(ctx context.Context, param *ibasic.ProductFilter) ([]*ibasic.Product, error) {
	if f.fetchProductsFn != nil {
		return f.fetchProductsFn(ctx, param)
	}
	return nil, nil
}

func (f *fakeProductStorager) DeleteProduct(ctx context.Context, p *ibasic.Product) error {
	return nil
}

func (f *fakeProductStorager) CreateProduct(ctx context.Context, p *ibasic.ProductParam) error {
	return nil
}

func (f *fakeProductStorager) UpdateProduct(ctx context.Context, p *ibasic.Product, newVal *ibasic.ProductParam) error {
	return nil
}

type fakeProductTxn struct{}

func (f *fakeProductTxn) AtomExecute(ctx context.Context, fn func(context.Context) error) error {
	return fn(ctx)
}

// setDefaultProductFallback 注入默认产品线配置与 ProductManager fake，
// 返回恢复函数。
func setDefaultProductFallback(name string) func() {
	oldConfig := stateful.DefaultConfig
	oldPM := container.ProductManager
	stateful.DefaultConfig = &stateful.Config{
		RunTime: stateful.RunTimeConfig{AIRouteInnerProductName: name},
	}
	container.ProductManager = ibasic.NewProductManager(&fakeProductTxn{}, &fakeProductStorager{
		fetchProductsFn: func(ctx context.Context, param *ibasic.ProductFilter) ([]*ibasic.Product, error) {
			return []*ibasic.Product{{ID: 9, Name: name}}, nil
		},
	})
	return func() {
		stateful.DefaultConfig = oldConfig
		container.ProductManager = oldPM
	}
}

func TestBatchTaskOneAction(t *testing.T) {
	t.Run("命中并组装关联文件", func(t *testing.T) {
		m := &fakeBatchManager{
			fetchTaskFn: func(ctx context.Context, batchID, productName string, allowRedisFallback bool) (*ibatch.BatchTask, error) {
				assert.Equal(t, "b-1", batchID)
				assert.Equal(t, "p1", productName)
				assert.True(t, allowRedisFallback)
				return &ibatch.BatchTask{
					BatchID: "b-1", Status: "in_progress",
					InputFileID: "f-in", OutputFileID: "f-out", Provider: "openai",
				}, nil
			},
			fetchFileFn: func(ctx context.Context, fileID, provider, productName string, allowRedisFallback bool) (*ibatch.BatchFile, error) {
				return &ibatch.BatchFile{
					FileID: fileID, Provider: provider,
					KeyName: "key-1", APIKeyID: "ak-1", ProductName: productName,
					Direction: "input", Purpose: "batch_input", Lines: 10, Bytes: 4224,
					CreatedAt: time.Date(2026, 10, 6, 9, 55, 0, 0, time.UTC),
				}, nil
			},
		}
		defer setupBatchManager(m)()

		req := withPathVars(newProductRequest(t, http.MethodGet, "/batches/b-1"), map[string]string{"batch_id": "b-1"})
		data, err := BatchTaskOneAction(req)
		require.NoError(t, err)
		// 契约（batches.md §3.2）：Data 顶层即任务字段，关联文件在 batch_files。
		task := data.(*ibatch.BatchTask)
		assert.Equal(t, "b-1", task.BatchID)
		require.Len(t, task.BatchFiles, 2)
		meta := task.BatchFiles[0]
		assert.Equal(t, "f-in", meta.FileID)
		assert.Equal(t, "openai", meta.Provider)
		assert.Equal(t, "batch_input", meta.Purpose)
		assert.Equal(t, int64(4224), meta.Bytes)
		assert.False(t, meta.CreatedAt.IsZero())
	})

	t.Run("输出文件缺失不阻塞详情", func(t *testing.T) {
		m := &fakeBatchManager{
			fetchTaskFn: func(ctx context.Context, batchID, productName string, allowRedisFallback bool) (*ibatch.BatchTask, error) {
				return &ibatch.BatchTask{
					BatchID: "b-2", Status: "in_progress",
					InputFileID: "f-in", Provider: "deepseek",
				}, nil
			},
			fetchFileFn: func(ctx context.Context, fileID, provider, productName string, allowRedisFallback bool) (*ibatch.BatchFile, error) {
				return &ibatch.BatchFile{FileID: fileID, Provider: provider}, nil
			},
		}
		defer setupBatchManager(m)()

		req := withPathVars(newProductRequest(t, http.MethodGet, "/batches/b-2"), map[string]string{"batch_id": "b-2"})
		data, err := BatchTaskOneAction(req)
		require.NoError(t, err)
		task := data.(*ibatch.BatchTask)
		require.Len(t, task.BatchFiles, 1)
		assert.Equal(t, "f-in", task.BatchFiles[0].FileID)
	})

	t.Run("未命中 404", func(t *testing.T) {
		defer setupBatchManager(&fakeBatchManager{})()
		req := withPathVars(newProductRequest(t, http.MethodGet, "/batches/ghost"), map[string]string{"batch_id": "ghost"})
		_, err := BatchTaskOneAction(req)
		require.Error(t, err)
		assert.True(t, errors.Is(err, ibatch.ErrBatchNotFound))
		assert.Equal(t, 404, xerror.Resolve(err).ErrNo)
	})
}

func TestBatchTaskCancelAction(t *testing.T) {
	t.Run("成功返回任务当前状态", func(t *testing.T) {
		m := &fakeBatchManager{
			cancelFn: func(ctx context.Context, batchID, productName string) (*ibatch.BatchTask, error) {
				return &ibatch.BatchTask{BatchID: batchID, Status: ibatch.TaskStatusCancelling}, nil
			},
		}
		defer setupBatchManager(m)()

		req := withPathVars(newProductRequest(t, http.MethodPost, "/batches/b-1/cancel"), map[string]string{"batch_id": "b-1"})
		data, err := BatchTaskCancelAction(req)
		require.NoError(t, err)
		task := data.(*ibatch.BatchTask)
		assert.Equal(t, ibatch.TaskStatusCancelling, task.Status)
	})

	t.Run("任务不存在 404", func(t *testing.T) {
		m := &fakeBatchManager{cancelFn: func(ctx context.Context, batchID, productName string) (*ibatch.BatchTask, error) {
			return nil, ibatch.ErrBatchNotFound
		}}
		defer setupBatchManager(m)()
		req := withPathVars(newProductRequest(t, http.MethodPost, "/batches/b-1/cancel"), map[string]string{"batch_id": "b-1"})
		_, err := BatchTaskCancelAction(req)
		assert.Equal(t, 404, xerror.Resolve(err).ErrNo)
	})

	t.Run("抢占冲突 409", func(t *testing.T) {
		m := &fakeBatchManager{cancelFn: func(ctx context.Context, batchID, productName string) (*ibatch.BatchTask, error) {
			return nil, xerror.WrapConflictErrorWithMsg("batch task %s is terminal", batchID)
		}}
		defer setupBatchManager(m)()
		req := withPathVars(newProductRequest(t, http.MethodPost, "/batches/b-1/cancel"), map[string]string{"batch_id": "b-1"})
		_, err := BatchTaskCancelAction(req)
		assert.Equal(t, 409, xerror.Resolve(err).ErrNo)
	})

	t.Run("provider 批量不存在 404", func(t *testing.T) {
		m := &fakeBatchManager{cancelFn: func(ctx context.Context, batchID, productName string) (*ibatch.BatchTask, error) {
			return nil, ibatch.ErrUpstreamBatchNotFound
		}}
		defer setupBatchManager(m)()
		req := withPathVars(newProductRequest(t, http.MethodPost, "/batches/b-1/cancel"), map[string]string{"batch_id": "b-1"})
		_, err := BatchTaskCancelAction(req)
		assert.Equal(t, 404, xerror.Resolve(err).ErrNo)
	})

	t.Run("provider 不可达 502", func(t *testing.T) {
		m := &fakeBatchManager{cancelFn: func(ctx context.Context, batchID, productName string) (*ibatch.BatchTask, error) {
			return nil, ibatch.ErrUpstreamUnreachable
		}}
		defer setupBatchManager(m)()
		req := withPathVars(newProductRequest(t, http.MethodPost, "/batches/b-1/cancel"), map[string]string{"batch_id": "b-1"})
		_, err := BatchTaskCancelAction(req)
		assert.Equal(t, 502, xerror.Resolve(err).ErrNo)
	})
}

func TestBatchFileOneAction(t *testing.T) {
	t.Run("provider 必填", func(t *testing.T) {
		defer setupBatchManager(&fakeBatchManager{})()
		req := withPathVars(newProductRequest(t, http.MethodGet, "/batch-files/f-1"), map[string]string{"file_id": "f-1"})
		_, err := BatchFileOneAction(req)
		require.Error(t, err)
		assert.Equal(t, 422, xerror.Resolve(err).ErrNo)
	})

	t.Run("命中并透传 product", func(t *testing.T) {
		m := &fakeBatchManager{
			fetchFileFn: func(ctx context.Context, fileID, provider, productName string, allowRedisFallback bool) (*ibatch.BatchFile, error) {
				assert.Equal(t, "f-1", fileID)
				assert.Equal(t, "openai", provider)
				assert.Equal(t, "p1", productName)
				return &ibatch.BatchFile{FileID: fileID, Provider: provider, ProductName: productName, Lines: 3}, nil
			},
		}
		defer setupBatchManager(m)()
		req := withPathVars(newProductRequest(t, http.MethodGet, "/batch-files/f-1?provider=openai"), map[string]string{"file_id": "f-1"})
		data, err := BatchFileOneAction(req)
		require.NoError(t, err)
		file := data.(*ibatch.BatchFile)
		assert.Equal(t, int64(3), file.Lines)
	})

	t.Run("未命中 404", func(t *testing.T) {
		defer setupBatchManager(&fakeBatchManager{})()
		req := withPathVars(newProductRequest(t, http.MethodGet, "/batch-files/ghost?provider=openai"), map[string]string{"file_id": "ghost"})
		_, err := BatchFileOneAction(req)
		assert.Equal(t, 404, xerror.Resolve(err).ErrNo)
	})
}

func TestEndpointsRegistration(t *testing.T) {
	paths := map[string]string{}
	for _, ep := range Endpoints {
		paths[ep.Path] = ep.Method
	}
	assert.Equal(t, http.MethodGet, paths["/batches"])
	assert.Equal(t, http.MethodGet, paths["/batches/{batch_id}"])
	assert.Equal(t, http.MethodPost, paths["/batches/{batch_id}/cancel"])
	assert.Equal(t, http.MethodGet, paths["/batch-files/{file_id}"])
	for _, ep := range Endpoints {
		require.NotNil(t, ep.Authorizer, "%s %s must have authorizer", ep.Method, ep.Path)
	}
}
