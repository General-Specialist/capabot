interface Option<T extends string> {
  value: T;
  label: string;
}

interface PillSwitchProps<T extends string> {
  options: Option<T>[];
  value: T;
  onChange: (value: T) => void;
}

function PillSwitch<T extends string>({ options, value, onChange }: PillSwitchProps<T>) {
  return (
    <div className="flex bg-icon-hover-white rounded-lg p-0.5 gap-0.5">
      {options.map((opt) => (
        <button
          key={opt.value}
          onClick={() => onChange(opt.value)}
          className={`px-2 py-0.5 rounded-md text-xs font-medium transition-colors ${
            value === opt.value
              ? "bg-white text-hover-black shadow-sm"
              : "text-normal-black hover:text-hover-black"
          }`}
        >
          {opt.label}
        </button>
      ))}
    </div>
  );
}

export default PillSwitch;
