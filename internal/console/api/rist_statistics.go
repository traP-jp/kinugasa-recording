package api

import "net/http"

func (h *Handler) listRISTStatistics(response http.ResponseWriter, request *http.Request) {
	sessionName := request.PathValue("sessionName")
	if _, err := h.service.GetSession(request.Context(), sessionName); err != nil {
		h.writeError(response, request, err)
		return
	}
	result := make([]ristStatisticsResponse, 0)
	if h.ristStatistics != nil {
		statistics := h.ristStatistics.List(sessionName)
		result = make([]ristStatisticsResponse, len(statistics))
		for index, item := range statistics {
			result[index] = newRISTStatisticsResponse(item)
		}
	}
	writeJSON(response, http.StatusOK, result)
}
