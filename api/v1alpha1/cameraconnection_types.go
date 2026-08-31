package v1alpha1

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

type CameraConnectionPhase string

const (
	CameraConnectionPhaseActivating CameraConnectionPhase = "Activating"
	CameraConnectionPhaseWaiting    CameraConnectionPhase = "Waiting"
	CameraConnectionPhaseConnected  CameraConnectionPhase = "Connected"
	CameraConnectionPhaseError      CameraConnectionPhase = "Error"
)

// CameraConnectionSpec identifies the durable domain objects for which the
// operator must provision one worker Pod and one shared PVC. Names are copied
// here because Kubernetes object names are not the authoritative user-facing
// resource names.
type CameraConnectionSpec struct {
	// SessionID is the immutable database identifier of the owning Session.
	// +kubebuilder:validation:Pattern=`^[0-9a-f]{8}-[0-9a-f]{4}-[1-8][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="sessionID is immutable"
	SessionID string `json:"sessionID"`

	// SessionName is the user-facing Session name used in recording paths.
	// +kubebuilder:validation:Pattern=`^[a-z](?:[a-z0-9-]{0,30}[a-z0-9])?$`
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="sessionName is immutable"
	SessionName string `json:"sessionName"`

	// CameraIdentityID is the immutable database identifier of the camera.
	// +kubebuilder:validation:Pattern=`^[0-9a-f]{8}-[0-9a-f]{4}-[1-8][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="cameraIdentityID is immutable"
	CameraIdentityID string `json:"cameraIdentityID"`

	// CameraName is the immutable user-facing camera name.
	// +kubebuilder:validation:Pattern=`^[a-z](?:[a-z0-9-]{0,30}[a-z0-9])?$`
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="cameraName is immutable"
	CameraName string `json:"cameraName"`
}

// +kubebuilder:validation:XValidation:rule="!has(self.phase) || ((self.phase == 'Error') == has(self.error))",message="error must be present exactly when phase is Error"
// +kubebuilder:validation:XValidation:rule="!has(self.phase) || self.phase == 'Activating' || has(self.cameraURL)",message="cameraURL must be present unless phase is Activating"
type CameraConnectionStatus struct {
	// ObservedGeneration is the latest spec generation reconciled into status.
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// Phase mirrors the CameraConnection state stored by console-server.
	// +kubebuilder:validation:Enum=Activating;Waiting;Connected;Error
	Phase CameraConnectionPhase `json:"phase,omitempty"`

	// CameraURL is assigned after the gateway endpoint has been provisioned.
	// +kubebuilder:validation:Format=uri
	CameraURL string `json:"cameraURL,omitempty"`

	// VideoWorkerID identifies the currently running video-worker process.
	// +kubebuilder:validation:Pattern=`^[0-9a-f]{8}-[0-9a-f]{4}-[1-8][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`
	VideoWorkerID string `json:"videoWorkerID,omitempty"`

	// WorkerPodName and PVCName expose the resources selected by the operator.
	WorkerPodName string `json:"workerPodName,omitempty"`
	PVCName       string `json:"pvcName,omitempty"`
	// WorkerPodUID and ObservedWorkerRestartCount make worker crash handling
	// idempotent across reconciliations and distinguish replacement Pods.
	WorkerPodUID               string `json:"workerPodUID,omitempty"`
	ObservedWorkerRestartCount int32  `json:"observedWorkerRestartCount,omitempty"`

	// Error is present exactly when Phase is Error.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=4096
	Error string `json:"error,omitempty"`

	// Conditions contain detailed reconciliation progress and failures.
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=krcamera
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Camera",type=string,JSONPath=`.spec.cameraName`
// +kubebuilder:printcolumn:name="Worker",type=string,JSONPath=`.status.workerPodName`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`
type CameraConnection struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   CameraConnectionSpec   `json:"spec"`
	Status CameraConnectionStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true
type CameraConnectionList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []CameraConnection `json:"items"`
}
