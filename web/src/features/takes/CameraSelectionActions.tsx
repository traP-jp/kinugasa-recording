import { Button } from "../../components/Button";

interface CameraSelectionActionsProps {
  allSelected: boolean;
  noneSelected: boolean;
  onSelectAll: () => void;
  onClear: () => void;
}

export function CameraSelectionActions({ allSelected, noneSelected, onSelectAll, onClear }: CameraSelectionActionsProps) {
  return (
    <div className="camera-selection-actions">
      <Button type="button" variant="quiet" disabled={allSelected} onClick={onSelectAll}>すべて選択</Button>
      <Button type="button" variant="quiet" disabled={noneSelected} onClick={onClear}>すべて解除</Button>
    </div>
  );
}
