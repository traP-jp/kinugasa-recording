package api

import "net/http"

func (h *Handler) createCamera(response http.ResponseWriter, request *http.Request) {
	var body createResourceRequest
	if err := decodeJSON(response, request, &body); err != nil {
		h.writeError(response, request, errInvalidRequest(err))
		return
	}
	camera, err := h.service.CreateCamera(
		request.Context(),
		request.PathValue("sessionName"),
		body.Name,
	)
	if err != nil {
		h.writeError(response, request, err)
		return
	}
	writeJSON(response, http.StatusCreated, newCameraConnectionResponse(camera))
}

func (h *Handler) listCameras(response http.ResponseWriter, request *http.Request) {
	cameras, err := h.service.ListCameras(request.Context(), request.PathValue("sessionName"))
	if err != nil {
		h.writeError(response, request, err)
		return
	}
	result := make([]cameraConnectionResponse, len(cameras))
	for index, camera := range cameras {
		result[index] = newCameraConnectionResponse(camera)
	}
	writeJSON(response, http.StatusOK, result)
}

func (h *Handler) getCameraConnection(response http.ResponseWriter, request *http.Request) {
	camera, err := h.service.GetCamera(
		request.Context(),
		request.PathValue("sessionName"),
		request.PathValue("cameraName"),
	)
	if err != nil {
		h.writeError(response, request, err)
		return
	}
	writeJSON(response, http.StatusOK, newCameraConnectionResponse(camera))
}

func (h *Handler) deleteCamera(response http.ResponseWriter, request *http.Request) {
	force, err := optionalBooleanQuery(request, "force")
	if err != nil {
		h.writeError(response, request, err)
		return
	}
	err = h.service.DeleteCamera(
		request.Context(),
		request.PathValue("sessionName"),
		request.PathValue("cameraName"),
		force,
	)
	if err != nil {
		h.writeError(response, request, err)
		return
	}
	response.WriteHeader(http.StatusNoContent)
}
