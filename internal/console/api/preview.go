package api

import "net/http"

func (h *Handler) createPreviewAccess(response http.ResponseWriter, request *http.Request) {
	access, err := h.service.CreatePreviewAccess(request.Context(), request.PathValue("sessionName"))
	if err != nil {
		h.writeError(response, request, err)
		return
	}
	writeJSON(response, http.StatusOK, previewAccessResponse{
		URL: access.URL, AccessToken: access.AccessToken, ExpiresAt: access.ExpiresAt,
	})
}
