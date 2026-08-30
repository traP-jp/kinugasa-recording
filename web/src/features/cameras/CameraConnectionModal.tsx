import { Copy, ExternalLink } from "lucide-react";
import { QRCodeCanvas } from "qrcode.react";
import { useState } from "react";
import type { CameraConnection } from "../../api/types";
import { Button } from "../../components/Button";
import { Modal } from "../../components/Modal";
import { StatusBadge } from "../../components/StatusBadge";

interface CameraConnectionModalProps {
  camera: CameraConnection;
  onClose: () => void;
}

export function CameraConnectionModal({ camera, onClose }: CameraConnectionModalProps) {
  const [copied, setCopied] = useState(false);
  const url = camera.url ?? "";
  async function copy() {
    await navigator.clipboard.writeText(url);
    setCopied(true);
    window.setTimeout(() => setCopied(false), 1600);
  }
  return (
    <Modal title={`${camera.name} 接続情報`} onClose={onClose}>
      <div className="connection-modal-body">
        <div className="connection-modal-status"><StatusBadge status={camera.status} /></div>
        {url ? (
          <>
            <div className="qr-frame"><QRCodeCanvas value={url} size={220} level="M" marginSize={2} /></div>
            <code className="connection-url">{url}</code>
            <Button variant="primary" icon={<Copy size={17} />} onClick={() => void copy()}>
              {copied ? "コピーしました" : "URLをコピー"}
            </Button>
            <p className="modal-hint"><ExternalLink size={14} />Camera clientでQRコードを読み取るか、URLを入力してください。</p>
          </>
        ) : <p>接続先を割り当てています。しばらくお待ちください。</p>}
      </div>
    </Modal>
  );
}
