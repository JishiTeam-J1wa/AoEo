// handler_models.go 实现 GET /v1/models 模型列表处理器。
//
// 返回 OpenAI 兼容的模型列表 JSON，遍历所有已配置的 Provider 并汇总其支持的模型。
//
// Author: JishiTeam-J1wa
// Created: 2026-06
package server

import (
	"encoding/json"
	"net/http"
)

// modelObject OpenAI 兼容的单个模型对象。
type modelObject struct {
	ID       string `json:"id"`
	Object   string `json:"object"`
	OwnedBy  string `json:"owned_by"`
	Created  int64  `json:"created,omitempty"`
}

// modelListResponse OpenAI 兼容的模型列表响应。
type modelListResponse struct {
	Object string        `json:"object"`
	Data   []modelObject `json:"data"`
}

// ModelsHandler 处理 GET /v1/models 请求。
//
// 返回 OpenAI 兼容的模型列表：
//
//	{
//	  "object": "list",
//	  "data": [
//	    {"id": "deepseek-chat", "object": "model", "owned_by": "deepseek"},
//	    ...
//	  ]
//	}
//
// 遍历所有 Provider 的状态，对每个可用的 Provider 调用 ListModels 收集模型信息。
// 查询单个 Provider 失败时不会中断整个列表，而是在日志中记录错误。
func (s *Server) ModelsHandler(w http.ResponseWriter, r *http.Request) {
	statuses := s.Client.ProviderStatus()

	var models []modelObject
	seen := make(map[string]bool)

	for _, ps := range statuses {
		if !ps.Available {
			continue
		}

		modelInfos, err := s.Client.ListModels(r.Context(), ps.Name)
		if err != nil {
			// 单个 Provider 查询失败不影响整体响应
			continue
		}

		for _, m := range modelInfos {
			// 去重：不同 Provider 可能返回相同模型
			if seen[m.ID] {
				continue
			}
			seen[m.ID] = true
			models = append(models, modelObject{
				ID:      m.ID,
				Object:  "model",
				OwnedBy: m.OwnedBy,
			})
		}
	}

	// 确保 data 字段不为 null
	if models == nil {
		models = []modelObject{}
	}

	resp := modelListResponse{
		Object: "list",
		Data:   models,
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(resp)
}
