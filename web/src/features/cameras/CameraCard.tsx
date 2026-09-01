import { Cable, QrCode, Trash2, Video } from "lucide-react";
import { useState } from "react";
import type { CameraConnection } from "../../api/types";
import { Button } from "../../components/Button";
import { StatusBadge } from "../../components/StatusBadge";
import { CameraConnectionModal } from "./CameraConnectionModal";
import { CameraDeletionConfirmation } from "./CameraDeletionConfirmation";

interface CameraCardProps {
  sessionName: string;
  camera: CameraConnection;
  deletionDisabled: boolean;
  onPrepareDelete: (name: string) => Promise<string[]>;
  onDelete: (name: string, force: boolean) => Promise<void>;
}

export function CameraCard({ sessionName, camera, deletionDisabled, onPrepareDelete, onDelete }: CameraCardProps) {
  const [modalOpen, setModalOpen] = useState(false);
  const [deletionOpen, setDeletionOpen] = useState(false);
  const [uploadingTakeNames, setUploadingTakeNames] = useState<string[]>([]);
  const [preparingDeletion, setPreparingDeletion] = useState(false);
  const [deleting, setDeleting] = useState(false);

  async function prepareRemoval() {
    setPreparingDeletion(true);
    try {
      setUploadingTakeNames(await onPrepareDelete(camera.name));
      setDeletionOpen(true);
    } catch {
      // The page-level error banner reports the failure.
    } finally {
      setPreparingDeletion(false);
    }
  }

  async function remove(force: boolean) {
    setDeleting(true);
    try {
      await onDelete(camera.name, force);
      setDeletionOpen(false);
    } catch {
      if (!force) {
        try {
          setUploadingTakeNames(await onPrepareDelete(camera.name));
        } catch {
          // The page-level error banner reports both failures.
        }
      }
    } finally {
      setDeleting(false);
    }
  }

  function cancelRemoval() {
    setDeletionOpen(false);
    setUploadingTakeNames([]);
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
          disabled={deletionDisabled || deleting || preparingDeletion}
          onClick={() => void prepareRemoval()}
        >{preparingDeletion ? "確認中…" : "削除"}</Button>
      </div>
      {modalOpen && <CameraConnectionModal sessionName={sessionName} camera={camera} onClose={() => setModalOpen(false)} />}
      {deletionOpen && (
        <CameraDeletionConfirmation
          cameraName={camera.name}
          uploadingTakeNames={uploadingTakeNames}
          deleting={deleting}
          onCancel={cancelRemoval}
          onConfirm={remove}
        />
      )}
    </article>
  );
}
