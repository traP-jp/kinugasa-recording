package api

import "net/http"

func (h *Handler) startTake(response http.ResponseWriter, request *http.Request) {
	var body startTakeRequest
	if err := decodeJSON(response, request, &body); err != nil {
		h.writeError(response, request, errInvalidRequest(err))
		return
	}
	take, err := h.service.StartTake(
		request.Context(), request.PathValue("sessionName"), body.Name, body.CameraNames,
	)
	if err != nil {
		h.writeError(response, request, err)
		return
	}
	writeJSON(response, http.StatusCreated, newOngoingTakeResponse(take))
}

func (h *Handler) getOngoingTake(response http.ResponseWriter, request *http.Request) {
	take, err := h.service.GetOngoingTake(request.Context(), request.PathValue("sessionName"))
	if err != nil {
		h.writeError(response, request, err)
		return
	}
	if take == nil {
		writeJSON(response, http.StatusOK, ongoingTakeResultResponse{Type: "absent"})
		return
	}
	takeResponse := newOngoingTakeResponse(*take)
	writeJSON(response, http.StatusOK, ongoingTakeResultResponse{Type: "present", OngoingTake: &takeResponse})
}

func (h *Handler) finishTake(response http.ResponseWriter, request *http.Request) {
	take, err := h.service.FinishTake(request.Context(), request.PathValue("sessionName"))
	if err != nil {
		h.writeError(response, request, err)
		return
	}
	writeJSON(response, http.StatusAccepted, newFinishedTakeResponse(take))
}
