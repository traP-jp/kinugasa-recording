package api

import "net/http"

func (h *Handler) createSession(response http.ResponseWriter, request *http.Request) {
	var body createResourceRequest
	if err := decodeJSON(response, request, &body); err != nil {
		h.writeError(response, request, errInvalidRequest(err))
		return
	}
	session, err := h.service.CreateSession(request.Context(), body.Name)
	if err != nil {
		h.writeError(response, request, err)
		return
	}
	writeJSON(response, http.StatusCreated, newSessionResponse(session))
}

func (h *Handler) listSessions(response http.ResponseWriter, request *http.Request) {
	pageRequest, err := pagination(request)
	if err != nil {
		h.writeError(response, request, err)
		return
	}
	page, err := h.service.ListSessions(request.Context(), pageRequest)
	if err != nil {
		h.writeError(response, request, err)
		return
	}
	writeJSON(response, http.StatusOK, newSessionPageResponse(page))
}

func (h *Handler) getSession(response http.ResponseWriter, request *http.Request) {
	detail, err := h.service.GetSession(request.Context(), request.PathValue("sessionName"))
	if err != nil {
		h.writeError(response, request, err)
		return
	}
	writeJSON(response, http.StatusOK, newSessionDetailResponse(detail))
}
