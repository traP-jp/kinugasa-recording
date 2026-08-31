import { Copy, ExternalLink } from "lucide-react";
import { QRCodeCanvas } from "qrcode.react";
import { useState } from "react";
import type { CameraConnection } from "../../api/types";
import { Button } from "../../components/Button";
import { Modal } from "../../components/Modal";
import { StatusBadge } from "../../components/StatusBadge";
import { buildMoblinUrl } from "../../lib/moblin";
import { ConnectionMethodTabs, type ConnectionMethod } from "./ConnectionMethodTabs";

interface CameraConnectionModalProps {
  camera: CameraConnection;
  onClose: () => void;
}

export function CameraConnectionModal({ camera, onClose }: CameraConnectionModalProps) {
  const [copied, setCopied] = useState(false);
  const [method, setMethod] = useState<ConnectionMethod>("moblin");
  const ristUrl = camera.url ?? "";
  const url = method === "moblin" && ristUrl ? buildMoblinUrl(camera.name, ristUrl) : ristUrl;
  async function copy() {
    await navigator.clipboard.writeText(url);
    setCopied(true);
    window.setTimeout(() => setCopied(false), 1600);
  }
  function selectMethod(value: ConnectionMethod) {
    setMethod(value);
    setCopied(false);
  }
  return (
    <Modal title={`${camera.name} 接続情報`} onClose={onClose}>
      <div className="connection-modal-body">
        <div className="connection-modal-status"><StatusBadge status={camera.status} /></div>
        {url ? (
          <>
            <ConnectionMethodTabs value={method} onChange={selectMethod} />
            <div className="qr-frame"><QRCodeCanvas value={url} size={220} level="M" marginSize={2} /></div>
            <code className="connection-url">{url}</code>
            <Button variant="primary" icon={<Copy size={17} />} onClick={() => void copy()}>
              {copied ? "コピーしました" : "URLをコピー"}
            </Button>
            <p className="modal-hint"><ExternalLink size={14} />{method === "moblin" ? "MoblinでQRコードを読み取って設定を取り込んでください。" : "Camera clientでQRコードを読み取るか、URLを入力してください。"}</p>
          </>
        ) : <p>接続先を割り当てています。しばらくお待ちください。</p>}
      </div>
    </Modal>
  );
}
