export type ConnectionMethod = "moblin" | "rist";

interface ConnectionMethodTabsProps {
  value: ConnectionMethod;
  onChange: (value: ConnectionMethod) => void;
}

const methods: Array<{ value: ConnectionMethod; label: string }> = [
  { value: "moblin", label: "Moblinで設定（推奨）" },
  { value: "rist", label: "RIST URL" },
];

export function ConnectionMethodTabs({ value, onChange }: ConnectionMethodTabsProps) {
  return (
    <div className="connection-method-tabs" role="tablist" aria-label="接続方法">
      {methods.map((method) => (
        <button
          key={method.value}
          type="button"
          role="tab"
          aria-selected={value === method.value}
          className={value === method.value ? "active" : ""}
          onClick={() => onChange(method.value)}
        >
          {method.label}
        </button>
      ))}
    </div>
  );
}
