import { Cable, QrCode, Trash2, TriangleAlert, Video } from "lucide-react";
import { useState } from "react";
import { Button } from "../../components/Button";
import { StatusBadge } from "../../components/StatusBadge";
import type { CameraDisplayState } from "./cameraDisplayState";
import { CameraConnectionModal } from "./CameraConnectionModal";
import { CameraDeletionConfirmation } from "./CameraDeletionConfirmation";
import { RISTStatisticsPanel } from "./RISTStatisticsPanel";

interface CameraCardProps {
  sessionName: string;
  cameraDisplay: CameraDisplayState;
  deletionDisabled: boolean;
  onPrepareDelete: (name: string) => Promise<string[]>;
  onDelete: (name: string, force: boolean) => Promise<void>;
}

export function CameraCard({ sessionName, cameraDisplay, deletionDisabled, onPrepareDelete, onDelete }: CameraCardProps) {
  const { camera, ristStatistics, packetRecoveryFailed, status } = cameraDisplay;
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
    <article className={`camera-card camera-${status}`}>
      <div className="camera-card-icon">
        {packetRecoveryFailed || camera.status === "error"
          ? <TriangleAlert size={20} />
          : camera.status === "connected" ? <Video size={20} /> : <Cable size={20} />}
      </div>
      <div className="camera-card-main">
        <div className="camera-card-title">
          <h3>{camera.name}</h3>
          <StatusBadge status={camera.status} />
          {packetRecoveryFailed && <StatusBadge status="packet-loss" />}
        </div>
        {camera.error && <p className="inline-error">{camera.error}</p>}
        <RISTStatisticsPanel statistics={ristStatistics} />
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
