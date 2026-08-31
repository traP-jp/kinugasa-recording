package cameraconnection

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"

	recordingv1alpha1 "github.com/traP-jp/kinugasa-recording/api/v1alpha1"
)

const ristSecretContext = "kinugasa-recording/rist/v1"

func deriveRISTSecret(connection *recordingv1alpha1.CameraConnection, pepper string) string {
	mac := hmac.New(sha256.New, []byte(pepper))
	_, _ = mac.Write([]byte(ristSecretContext))
	_, _ = mac.Write([]byte{0})
	_, _ = mac.Write([]byte(connection.Spec.SessionID))
	_, _ = mac.Write([]byte{0})
	_, _ = mac.Write([]byte(connection.Spec.CameraIdentityID))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}
