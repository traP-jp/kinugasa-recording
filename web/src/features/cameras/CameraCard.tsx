import { Cable, QrCode, Trash2, Video } from "lucide-react";
import { useState } from "react";
import type { CameraConnection } from "../../api/types";
import { Button } from "../../components/Button";
import { StatusBadge } from "../../components/StatusBadge";
import { CameraConnectionModal } from "./CameraConnectionModal";
import { CameraDeletionDialog } from "./CameraDeletionDialog";

interface CameraCardProps {
  camera: CameraConnection;
  deletionDisabled: boolean;
  uploadingTakeNames: string[];
  onDelete: (name: string, force: boolean) => Promise<void>;
}

export function CameraCard({ camera, deletionDisabled, uploadingTakeNames, onDelete }: CameraCardProps) {
  const [modalOpen, setModalOpen] = useState(false);
  const [deletionOpen, setDeletionOpen] = useState(false);
  const [deleting, setDeleting] = useState(false);
  async function remove(force: boolean) {
    setDeleting(true);
    try {
      await onDelete(camera.name, force);
      setDeletionOpen(false);
    } finally {
      setDeleting(false);
    }
  }
  return (
    <article className={`camera-card camera-${camera.status}`}>
      <div className="camera-card-icon">{camera.status === "connected" ? <Video size={20} /> : <Cable size={20} />}</div>
      <div className="camera-card-main">
        <div className="camera-card-title"><h3>{camera.name}</h3><StatusBadge status={camera.status} /></div>
        {camera.error && <p className="inline-error">{camera.error}</p>}
        {!camera.error && <p>{camera.status === "connected" ? "映像を受信しています" : "Camera clientの接続を待っています"}</p>}
      </div>
      <div className="camera-card-actions">
        <Button
          variant="quiet"
          icon={<QrCode size={17} />}
          disabled={camera.status === "activating" || !camera.url}
          onClick={() => setModalOpen(true)}
        >接続</Button>
        <Button
          variant="quiet"
          icon={<Trash2 size={17} />}
          disabled={deletionDisabled || deleting}
          onClick={() => setDeletionOpen(true)}
        >削除</Button>
      </div>
      {modalOpen && <CameraConnectionModal camera={camera} onClose={() => setModalOpen(false)} />}
      {deletionOpen && (
        <CameraDeletionDialog
          cameraName={camera.name}
          uploadingTakeNames={uploadingTakeNames}
          deleting={deleting}
          onCancel={() => setDeletionOpen(false)}
          onConfirm={remove}
        />
      )}
    </article>
  );
}
