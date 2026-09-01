package api

import (
	"encoding/json"
	"net/http"
)

func (h *Handler) getLockfile(response http.ResponseWriter, request *http.Request) {
	lockfile, err := h.service.GetLockfile(request.Context(), request.PathValue("sessionName"))
	if err != nil {
		h.writeError(response, request, err)
		return
	}
	response.Header().Set("Content-Type", "text/plain; charset=utf-8")
	response.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(response).Encode(lockfile)
}
