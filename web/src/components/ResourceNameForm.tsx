import { Plus } from "lucide-react";
import { useState, type FormEvent } from "react";
import { Button } from "./Button";

interface ResourceNameFormProps {
  label: string;
  placeholder: string;
  actionLabel: string;
  disabled?: boolean;
  onSubmit: (name: string) => Promise<void>;
}

const resourceName = /^[a-z](?:[a-z0-9-]{0,30}[a-z0-9])?$/;

export function ResourceNameForm({ label, placeholder, actionLabel, disabled, onSubmit }: ResourceNameFormProps) {
  const [name, setName] = useState("");
  const [submitting, setSubmitting] = useState(false);
  const valid = resourceName.test(name);

  async function submit(event: FormEvent) {
    event.preventDefault();
    if (!valid || submitting) return;
    setSubmitting(true);
    try {
      await onSubmit(name);
      setName("");
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <form className="resource-form" onSubmit={(event) => void submit(event)}>
      <label>
        <span>{label}</span>
        <input
          value={name}
          onChange={(event) => setName(event.target.value)}
          placeholder={placeholder}
          maxLength={32}
          autoComplete="off"
          disabled={disabled || submitting}
        />
      </label>
      <Button type="submit" variant="primary" icon={<Plus size={17} />} disabled={disabled || submitting || !valid}>
        {submitting ? "処理中…" : actionLabel}
      </Button>
      {name && !valid && <small>a-zで始まる32文字以内の英小文字・数字・ハイフンを使用してください。</small>}
    </form>
  );
}
