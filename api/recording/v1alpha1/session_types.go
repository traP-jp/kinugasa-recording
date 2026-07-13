package v1alpha1

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

const (
	NamePattern = `^[A-Za-z0-9-]+$`
	MaxNameSize = 255
)

type DesiredState string

const (
	DesiredStatePresent   DesiredState = "Present"
	DesiredStateAbsent    DesiredState = "Absent"
	DesiredStateRecording DesiredState = "Recording"
	DesiredStateStopped   DesiredState = "Stopped"
)

type SessionPhase string

const (
	SessionPhasePending   SessionPhase = "Pending"
	SessionPhaseReady     SessionPhase = "Ready"
	SessionPhaseRecording SessionPhase = "Recording"
	SessionPhaseDegraded  SessionPhase = "Degraded"
)

type CameraPhase string

const (
	CameraPhaseProvisioning CameraPhase = "Provisioning"
	CameraPhaseWaiting      CameraPhase = "Waiting"
	CameraPhaseConnected    CameraPhase = "Connected"
	CameraPhaseDisconnected CameraPhase = "Disconnected"
	CameraPhaseDeleting     CameraPhase = "Deleting"
	CameraPhaseRemoved      CameraPhase = "Removed"
	CameraPhaseError        CameraPhase = "Error"
)

type TakePhase string

const (
	TakePhasePending   TakePhase = "Pending"
	TakePhaseStarting  TakePhase = "Starting"
	TakePhaseRecording TakePhase = "Recording"
	TakePhaseStopping  TakePhase = "Stopping"
	TakePhaseUploading TakePhase = "Uploading"
	TakePhaseCompleted TakePhase = "Completed"
	TakePhaseFailed    TakePhase = "Failed"
)

type ProcessPhase string

const (
	ProcessPhasePending ProcessPhase = "Pending"
	ProcessPhaseRunning ProcessPhase = "Running"
	ProcessPhaseStopped ProcessPhase = "Stopped"
	ProcessPhaseFailed  ProcessPhase = "Failed"
)

type UploadPhase string

const (
	UploadPhasePending   UploadPhase = "Pending"
	UploadPhaseUploading UploadPhase = "Uploading"
	UploadPhaseCompleted UploadPhase = "Completed"
	UploadPhaseFailed    UploadPhase = "Failed"
)

// SessionSpec describes the desired recording session state.
// +kubebuilder:validation:XValidation:rule="self.name == oldSelf.name",message="name is immutable"
// +kubebuilder:validation:XValidation:rule="!has(oldSelf.reservedCameraNames) || oldSelf.reservedCameraNames.all(n, n in self.reservedCameraNames)",message="reserved camera names cannot be removed"
// +kubebuilder:validation:XValidation:rule="!has(oldSelf.reservedTakeNames) || oldSelf.reservedTakeNames.all(n, n in self.reservedTakeNames)",message="reserved take names cannot be removed"
// +kubebuilder:validation:XValidation:rule="!has(self.cameras) || (has(self.reservedCameraNames) && self.cameras.all(c, c.name in self.reservedCameraNames))",message="camera names must be reserved"
// +kubebuilder:validation:XValidation:rule="!has(self.takes) || (has(self.reservedTakeNames) && self.takes.all(t, t.name in self.reservedTakeNames))",message="take names must be reserved"
type SessionSpec struct {
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=255
	// +kubebuilder:validation:Pattern=`^[A-Za-z0-9-]+$`
	Name string `json:"name"`

	// +listType=set
	// +kubebuilder:validation:items:MinLength=1
	// +kubebuilder:validation:items:MaxLength=255
	// +kubebuilder:validation:items:Pattern=`^[A-Za-z0-9-]+$`
	ReservedCameraNames []string `json:"reservedCameraNames,omitempty"`

	// +listType=set
	// +kubebuilder:validation:items:MinLength=1
	// +kubebuilder:validation:items:MaxLength=255
	// +kubebuilder:validation:items:Pattern=`^[A-Za-z0-9-]+$`
	ReservedTakeNames []string `json:"reservedTakeNames,omitempty"`

	// +listType=map
	// +listMapKey=name
	Cameras []CameraSpec `json:"cameras,omitempty"`

	// +listType=map
	// +listMapKey=name
	Takes []TakeSpec `json:"takes,omitempty"`
}

type CameraSpec struct {
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=255
	// +kubebuilder:validation:Pattern=`^[A-Za-z0-9-]+$`
	Name string `json:"name"`

	// +kubebuilder:validation:Enum=Present;Absent
	DesiredState DesiredState `json:"desiredState"`

	Ingress CameraIngressSpec `json:"ingress"`
}

type CameraIngressSpec struct {
	// +kubebuilder:validation:Minimum=30000
	// +kubebuilder:validation:Maximum=32767
	RISTNodePort int32 `json:"ristNodePort"`

	// +kubebuilder:validation:Minimum=30000
	// +kubebuilder:validation:Maximum=32767
	SRTNodePort int32 `json:"srtNodePort"`
}

type TakeSpec struct {
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=255
	// +kubebuilder:validation:Pattern=`^[A-Za-z0-9-]+$`
	Name string `json:"name"`

	// +kubebuilder:validation:Enum=Recording;Stopped
	DesiredState DesiredState `json:"desiredState"`

	// +listType=set
	// +kubebuilder:validation:items:MinLength=1
	// +kubebuilder:validation:items:MaxLength=255
	// +kubebuilder:validation:items:Pattern=`^[A-Za-z0-9-]+$`
	CameraNames     []string     `json:"cameraNames"`
	RequestedAt     metav1.Time  `json:"requestedAt"`
	StopRequestedAt *metav1.Time `json:"stopRequestedAt,omitempty"`
}

type SessionStatus struct {
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// +kubebuilder:validation:Enum=Pending;Ready;Recording;Degraded
	Phase SessionPhase `json:"phase,omitempty"`

	// +listType=map
	// +listMapKey=name
	Cameras []CameraStatus `json:"cameras,omitempty"`

	// +listType=map
	// +listMapKey=name
	Takes []TakeStatus `json:"takes,omitempty"`

	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

type CameraStatus struct {
	Name string `json:"name"`

	// +kubebuilder:validation:Enum=Provisioning;Waiting;Connected;Disconnected;Deleting;Removed;Error
	Phase CameraPhase `json:"phase,omitempty"`

	// +kubebuilder:validation:Enum=rist;srt
	ConnectedProtocol string          `json:"connectedProtocol,omitempty"`
	LastFrameAt       *metav1.Time    `json:"lastFrameAt,omitempty"`
	Endpoints         CameraEndpoints `json:"endpoints,omitempty"`

	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

type CameraEndpoints struct {
	RIST string `json:"rist,omitempty"`
	SRT  string `json:"srt,omitempty"`
}

type TakeStatus struct {
	Name string `json:"name"`

	// +kubebuilder:validation:Enum=Pending;Starting;Recording;Stopping;Uploading;Completed;Failed
	Phase TakePhase `json:"phase,omitempty"`

	// +listType=map
	// +listMapKey=name
	Cameras []TakeCameraStatus `json:"cameras,omitempty"`

	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

type TakeCameraStatus struct {
	Name string `json:"name"`

	// +kubebuilder:validation:Enum=Pending;Running;Stopped;Failed
	RecorderPhase ProcessPhase `json:"recorderPhase,omitempty"`

	// +kubebuilder:validation:Enum=Pending;Uploading;Completed;Failed
	UploadPhase UploadPhase `json:"uploadPhase,omitempty"`

	// +kubebuilder:validation:Minimum=0
	DiscoveredFiles int32 `json:"discoveredFiles,omitempty"`
	// +kubebuilder:validation:Minimum=0
	UploadedFiles int32 `json:"uploadedFiles,omitempty"`
	// +kubebuilder:validation:Minimum=0
	PendingFiles int32 `json:"pendingFiles,omitempty"`
	// +kubebuilder:validation:Minimum=0
	FailedFiles           int32  `json:"failedFiles,omitempty"`
	LastUploadedObjectKey string `json:"lastUploadedObjectKey,omitempty"`

	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:resource:shortName=krsession
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`
// +genclient
type Session struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   SessionSpec   `json:"spec"`
	Status SessionStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true
type SessionList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Session `json:"items"`
}
