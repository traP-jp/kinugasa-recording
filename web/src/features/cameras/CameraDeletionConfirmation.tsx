import { AlertTriangle } from "lucide-react";
import { useState } from "react";
import { Button } from "../../components/Button";

export const CAMERA_FORCE_DELETE_CONFIRMATION =
  "I_do_understand_the_danger_of_data_inconsistency_and_really_want_to_delete_this_camera";

interface CameraDeletionConfirmationProps {
  cameraName: string;
  uploadingTakeNames: string[];
  deleting: boolean;
  onCancel: () => void;
  onConfirm: (force: boolean) => Promise<void>;
}

export function CameraDeletionConfirmation({
  cameraName,
  uploadingTakeNames,
  deleting,
  onCancel,
  onConfirm,
}: CameraDeletionConfirmationProps) {
  const [confirmation, setConfirmation] = useState("");
  const dangerous = uploadingTakeNames.length > 0;
  const confirmed = !dangerous || confirmation === CAMERA_FORCE_DELETE_CONFIRMATION;

  return (
    <section className="camera-deletion-confirmation" aria-label={`${cameraName} の削除確認`}>
      {dangerous ? (
        <>
          <div className="danger-message">
            <AlertTriangle size={22} />
            <div>
              <strong>アップロード中の動画があります</strong>
              <p>
                削除すると upload は中断され、対象 VideoFile は errored になります。
                オブジェクトストレージ上のデータ整合性は保証されません。
              </p>
            </div>
          </div>
          <div className="affected-video-files">
            <span>影響を受ける VideoFile</span>
            <ul>{uploadingTakeNames.map((name) => <li key={name}>{name} / {cameraName} / video.mp4</li>)}</ul>
          </div>
          <label className="danger-confirmation">
            <span>続行するには次の文字列を入力してください</span>
            <code>{CAMERA_FORCE_DELETE_CONFIRMATION}</code>
            <input
              autoComplete="off"
              spellCheck={false}
              value={confirmation}
              onChange={(event) => setConfirmation(event.target.value)}
            />
          </label>
        </>
      ) : (
        <div className="camera-delete-message">
          <strong>{cameraName} を削除しますか？</strong>
          <p>この camera と対応する video worker を削除します。</p>
        </div>
      )}
      <div className="deletion-actions">
        <Button variant="quiet" disabled={deleting} onClick={onCancel}>キャンセル</Button>
        <Button
          variant="danger"
          disabled={!confirmed || deleting}
          onClick={() => void onConfirm(dangerous)}
        >{deleting ? "削除中…" : dangerous ? "強制削除" : "削除"}</Button>
      </div>
    </section>
  );
}
