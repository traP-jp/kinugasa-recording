import type { ReactNode } from "react";
import { Modal } from "../../components/Modal";

interface VideoPreviewModalProps {
  cameraName: string;
  children: ReactNode;
  onClose: () => void;
}

export function VideoPreviewModal({ cameraName, children, onClose }: VideoPreviewModalProps) {
  return (
    <Modal title={cameraName} className="video-preview-modal" onClose={onClose}>
      <div className="video-preview-modal-body">{children}</div>
    </Modal>
  );
}
