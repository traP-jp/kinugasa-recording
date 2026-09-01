import { Check, Copy } from "lucide-react";
import { useState } from "react";
import { api } from "../../api/client";
import { Button } from "../../components/Button";

interface LockfileCopyButtonProps {
  sessionName: string;
  onError: (error: Error | null) => void;
}

export function LockfileCopyButton({ sessionName, onError }: LockfileCopyButtonProps) {
  const [copying, setCopying] = useState(false);
  const [copied, setCopied] = useState(false);

  async function copyLockfile() {
    setCopying(true);
    setCopied(false);
    onError(null);
    try {
      const lockfile = await api.getSessionLockfile(sessionName);
      await navigator.clipboard.writeText(lockfile);
      setCopied(true);
      window.setTimeout(() => setCopied(false), 1600);
    } catch (error) {
      onError(error instanceof Error ? error : new Error(String(error)));
    } finally {
      setCopying(false);
    }
  }

  return (
    <Button
      variant="quiet"
      icon={copied ? <Check size={17} /> : <Copy size={17} />}
      disabled={copying}
      onClick={() => void copyLockfile()}
    >
      {copying ? "取得中…" : copied ? "コピーしました" : "Lockfileをコピー"}
    </Button>
  );
}
