import React, { useState, useRef, useEffect } from "react";
import { createPortal } from "react-dom";
import {
  useFloating,
  offset,
  flip,
  shift,
  autoUpdate,
} from "@floating-ui/react-dom";
import {
  Calendar as CalendarIcon,
  ChevronDown,
  Calendar1,
  Infinity,
  Sun,
  SunMoon,
  Moon,
  CalendarRange,
  Clock,
} from "lucide-react";
import Calendar from "./Calendar";
import PillSwitch from "./PillSwitch";

interface DatePickerProps {
  start_at?: string | null;
  end_at?: string | null;
  start_offset?: string | null;
  end_offset?: string | null;
  rrule?: string | null;
  showRepeat?: boolean;
  onChange?: (data: {
    start_at?: string | null;
    end_at?: string | null;
    start_offset?: string | null;
    end_offset?: string | null;
    rrule?: string | null;
  }) => void;
}

type RelativeUnit = "days" | "weeks" | "months" | "years";

interface DateState {
  abs: Date | null;
  offset: string | null;
  relValue: number | string;
  relUnit: RelativeUnit;
}

interface PickerState {
  start: DateState;
  end: DateState;
  rrule: string | null;
  interval: string;
  byWeekday: string[];
  byTime: string | null; // "HH:MM" or null
}

const parseISO = (iso?: string | null): { value: number; unit: RelativeUnit } | "today" | null => {
  if (!iso) return null;
  if (iso === "P0D") return "today";
  const match = iso.match(/^P(\d+)([DWMY])$/);
  if (!match) return null;
  const value = parseInt(match[1]);
  const unit = { D: "days", W: "weeks", M: "months", Y: "years" }[match[2]] as RelativeUnit;
  return { value, unit };
};

const toISO = (value: number, unit: RelativeUnit): string => {
  const code = { days: "D", weeks: "W", months: "M", years: "Y" }[unit];
  return `P${value}${code}`;
};

const formatDate = (abs?: string | null, offset?: string | null): string => {
  const parsed = parseISO(offset);
  if (parsed === "today") return "Today (always)";
  if (parsed) return `${parsed.value} ${parsed.unit} from now`;
  if (abs) {
    const d = new Date(abs);
    const today = new Date();
    if (d.toDateString() === today.toDateString()) return "Today";
    return d.toLocaleDateString("en-US", {
      month: "short",
      day: "numeric",
      year: d.getFullYear() !== today.getFullYear() ? "numeric" : undefined,
    });
  }
  return "Not set";
};

const getIcon = (freq: string, active: boolean) => {
  const c = `w-3.5 h-3.5 ${active ? "text-white" : "text-brand-primary"}`;
  if (freq === "days" || freq === "DAILY") return <Sun className={c} />;
  if (freq === "weeks" || freq === "WEEKLY") return <SunMoon className={c} />;
  if (freq === "months" || freq === "MONTHLY") return <Moon className={c} />;
  if (freq === "years" || freq === "YEARLY") return <CalendarRange className={c} />;
  return null;
};

const DateEditor: React.FC<{
  label: string;
  state: DateState;
  onChange: (updates: Partial<DateState>) => void;
  otherAbs?: Date | null;
  isNoEnd?: boolean;
  toggle?: { label: string; value: boolean; onChange: (v: boolean) => void };
}> = ({ label, state, onChange, otherAbs, isNoEnd, toggle }) => {
  const mode = state.offset ? "relative" : "absolute";
  const parsed = parseISO(state.offset);
  const isToday = parsed === "today";

  return (
    <div className="flex flex-col p-2" style={{ width: "266px" }}>
      <div className="flex items-center justify-between mb-3">
        <div className="text-14 text-hover-black">{label}</div>
        <PillSwitch
          options={[
            { value: "absolute" as const, label: "A" },
            { value: "relative" as const, label: "R" },
          ]}
          value={mode}
          onChange={(m) => {
            if (m === "absolute") {
              onChange({ abs: state.abs || new Date(), offset: null });
            } else {
              const numVal = typeof state.relValue === "number" ? state.relValue : parseInt(state.relValue, 10) || 30;
              onChange({ abs: null, offset: toISO(numVal, state.relUnit) });
            }
          }}
        />
      </div>
      <div className="flex-1 flex flex-col" style={{ minHeight: "280px" }}>
        {mode === "absolute" && !isToday && !isNoEnd && (
          <Calendar
            selected={state.abs || new Date()}
            onSelect={(d) => !toggle?.value && onChange({ abs: d || new Date() })}
            rangeStart={state.abs && otherAbs ? (state.abs < otherAbs ? state.abs : otherAbs) : undefined}
            rangeEnd={state.abs && otherAbs ? (state.abs < otherAbs ? otherAbs : state.abs) : undefined}
          />
        )}
        {mode === "absolute" && isNoEnd && (
          <div className="flex flex-col items-center justify-center h-full text-hover-black">
            <Infinity className="w-12 h-12 mb-2" />
            <span className="text-14">No End Date</span>
          </div>
        )}
        {mode === "relative" && !isToday && (
          <div className="space-y-3">
            <div className="flex items-center gap-2">
              <input
                type="number"
                min="1"
                value={state.relValue}
                onChange={(e) => onChange({ relValue: e.target.value })}
                onBlur={(e) => {
                  const num = parseInt(e.target.value, 10);
                  const validated = !isNaN(num) && num > 0 ? num : 30;
                  onChange({ relValue: validated, offset: toISO(validated, state.relUnit) });
                }}
                className="w-20 px-3 py-2 bg-white border border-border-white rounded-lg text-14 text-hover-black"
              />
              <span className="text-14 text-hover-black">{state.relUnit} from now</span>
            </div>
            {(["days", "weeks", "months", "years"] as RelativeUnit[]).map((u) => (
              <button
                key={u}
                onClick={() => {
                  const numVal = typeof state.relValue === "number" ? state.relValue : parseInt(state.relValue, 10) || 30;
                  onChange({ relUnit: u, offset: toISO(numVal, u) });
                }}
                className={`w-full px-3 py-2 rounded-lg text-14 text-left flex items-center gap-2 ${
                  state.relUnit === u
                    ? "bg-brand-primary text-white"
                    : "bg-icon-hover-white text-hover-black hover:bg-sidebar-hover-white"
                }`}
              >
                {getIcon(u, state.relUnit === u)}
                {u.charAt(0).toUpperCase() + u.slice(1)}
              </button>
            ))}
          </div>
        )}
        {isToday && (
          <div className="flex flex-col items-center justify-center h-full text-hover-black">
            <Calendar1 className="w-12 h-12 mb-2" />
            <span className="text-14">Always Today</span>
          </div>
        )}
      </div>
      {toggle && (
        <div className="flex items-center justify-between mt-3 pt-3 border-t border-border-white">
          <div className="flex items-center gap-2">
            {toggle.label === "Start Today" && <Calendar1 className="w-3.5 h-3.5 text-brand-primary" />}
            {toggle.label === "No End" && <Infinity className="w-3.5 h-3.5 text-brand-primary" />}
            <span className="text-14 text-hover-black">{toggle.label}</span>
          </div>
          <button
            onClick={() => toggle.onChange(!toggle.value)}
            className={`relative w-10 h-5 rounded-full transition-colors ${toggle.value ? "bg-brand-primary" : "bg-icon-hover-white"}`}
          >
            <div className={`absolute top-0.5 w-4 h-4 bg-white rounded-full transition-transform ${toggle.value ? "translate-x-5" : "translate-x-0.5"}`} />
          </button>
        </div>
      )}
    </div>
  );
};

const getInitialState = (props: {
  start_at?: string | null;
  end_at?: string | null;
  start_offset?: string | null;
  end_offset?: string | null;
  rrule?: string | null;
}): PickerState => {
  const parseRRule = (rrule: string | null | undefined): { freq: string | null; interval: string; byWeekday: string[]; byTime: string | null } => {
    if (!rrule) return { freq: null, interval: "1", byWeekday: [], byTime: null };
    const intervalMatch = rrule.match(/INTERVAL=(\d+)/);
    const freqMatch = rrule.match(/FREQ=(DAILY|WEEKLY|MONTHLY|YEARLY)/);
    const byDayMatch = rrule.match(/BYDAY=([A-Z,]+)/);
    const byHourMatch = rrule.match(/BYHOUR=(\d+)/);
    const byMinuteMatch = rrule.match(/BYMINUTE=(\d+)/);
    let byTime: string | null = null;
    if (byHourMatch) {
      const h = byHourMatch[1].padStart(2, "0");
      const m = byMinuteMatch ? byMinuteMatch[1].padStart(2, "0") : "00";
      byTime = `${h}:${m}`;
    }
    return {
      freq: freqMatch?.[1] || null,
      interval: intervalMatch?.[1] || "1",
      byWeekday: byDayMatch ? byDayMatch[1].split(",") : [],
      byTime,
    };
  };

  const startParsed = parseISO(props.start_offset);
  const endParsed = parseISO(props.end_offset);
  const { freq, interval, byWeekday, byTime } = parseRRule(props.rrule);

  return {
    start: {
      abs: props.start_at ? new Date(props.start_at) : null,
      offset: props.start_offset || null,
      relValue: startParsed && startParsed !== "today" ? startParsed.value : 30,
      relUnit: startParsed && startParsed !== "today" ? startParsed.unit : "days",
    },
    end: {
      abs: props.end_at ? new Date(props.end_at) : null,
      offset: props.end_offset || null,
      relValue: endParsed && endParsed !== "today" ? endParsed.value : 30,
      relUnit: endParsed && endParsed !== "today" ? endParsed.unit : "days",
    },
    rrule: freq,
    interval,
    byWeekday,
    byTime,
  };
};

const DatePicker: React.FC<DatePickerProps> = ({
  start_at,
  end_at,
  start_offset,
  end_offset,
  rrule: recurrenceRule,
  showRepeat = true,
  onChange,
}) => {
  const [isOpen, setIsOpen] = useState(false);
  const triggerRef = useRef<HTMLButtonElement>(null);
  const popupRef = useRef<HTMLDivElement>(null);

  const { x, y, strategy, refs } = useFloating({
    middleware: [offset(8), flip(), shift({ padding: 8 })],
    whileElementsMounted: autoUpdate,
    placement: "bottom-start",
  });

  const [state, setState] = useState<PickerState>(() =>
    getInitialState({ start_at, end_at, start_offset, end_offset, rrule: recurrenceRule })
  );

  useEffect(() => {
    if (isOpen) return;
    setState(getInitialState({ start_at, end_at, start_offset, end_offset, rrule: recurrenceRule }));
  }, [start_at, start_offset, end_at, end_offset, recurrenceRule, isOpen]);

  useEffect(() => {
    const handleClick = (e: MouseEvent) => {
      if (
        popupRef.current && !popupRef.current.contains(e.target as Node) &&
        triggerRef.current && !triggerRef.current.contains(e.target as Node)
      ) {
        setIsOpen(false);
      }
    };
    if (isOpen) document.addEventListener("mousedown", handleClick);
    return () => document.removeEventListener("mousedown", handleClick);
  }, [isOpen]);

  useEffect(() => {
    if (triggerRef.current) refs.setReference(triggerRef.current);
  }, [refs]);

  const isStartToday = state.start.offset === "P0D";
  const isNoEnd = !state.end.abs && !state.end.offset;

  const displayStartAbs = state.start.abs && !state.start.offset ? state.start.abs.toISOString() : null;
  const displayEndAbs = state.end.abs && !state.end.offset ? state.end.abs.toISOString() : null;

  const formatRRule = (freq: string | null, int: string, byWeekday: string[], byTime: string | null): string => {
    if (!freq) return "";
    const freqMap: Record<string, string> = { DAILY: "day", WEEKLY: "week", MONTHLY: "month", YEARLY: "year" };
    const unit = freqMap[freq] || freq.toLowerCase();
    const n = parseInt(int);
    let baseText = n === 1 ? unit : `${n} ${unit}s`;
    if (freq === "WEEKLY" && byWeekday.length > 0) {
      const dayMap: Record<string, string> = { SU: "Sun", MO: "Mon", TU: "Tue", WE: "Wed", TH: "Thu", FR: "Fri", SA: "Sat" };
      baseText = `${baseText} on ${byWeekday.map((d) => dayMap[d]).join(", ")}`;
    }
    if (byTime) {
      const [h, m] = byTime.split(":").map(Number);
      const ampm = h >= 12 ? "PM" : "AM";
      const h12 = h % 12 || 12;
      baseText = `${baseText} at ${h12}:${String(m).padStart(2, "0")} ${ampm}`;
    }
    return baseText;
  };

  const displayText = state.rrule
    ? `${formatDate(displayStartAbs, state.start.offset)} - ${displayEndAbs || state.end.offset ? formatDate(displayEndAbs, state.end.offset) : "∞"} (every ${formatRRule(state.rrule, state.interval, state.byWeekday, state.byTime)})`
    : `${formatDate(displayStartAbs, state.start.offset)}${displayEndAbs || state.end.offset ? ` - ${formatDate(displayEndAbs, state.end.offset)}` : isNoEnd ? " - ∞" : ""}`;

  const handleApply = () => {
    let finalRRule = null;
    if (state.rrule) {
      const parts = [`FREQ=${state.rrule}`];
      if (state.interval !== "1") parts.push(`INTERVAL=${state.interval}`);
      if (state.rrule === "WEEKLY" && state.byWeekday.length > 0) parts.push(`BYDAY=${state.byWeekday.join(",")}`);
      if (state.byTime) {
        const [h, m] = state.byTime.split(":").map(Number);
        parts.push(`BYHOUR=${h}`);
        parts.push(`BYMINUTE=${m}`);
      }
      finalRRule = parts.join(";");
    }
    onChange?.({
      start_at: state.start.abs && !state.start.offset ? state.start.abs.toISOString() : null,
      start_offset: state.start.offset || null,
      end_at: state.end.abs && !state.end.offset ? state.end.abs.toISOString() : null,
      end_offset: state.end.offset || null,
      rrule: finalRRule,
    });
    setIsOpen(false);
  };

  return (
    <div className="w-full flex-shrink-0 relative">
      <div className="flex items-center">
        <CalendarIcon className="w-4 h-4 text-hover-black mr-2" />
        <button
          ref={triggerRef}
          onClick={() => setIsOpen(!isOpen)}
          className="text-14 flex items-center gap-2 py-1.5 px-2 rounded-lg hover:bg-icon-hover-white transition-colors"
        >
          <span className="text-hover-black">{displayText}</span>
          <ChevronDown className={`w-4 h-4 text-hover-black transition-transform ${isOpen ? "rotate-180" : ""}`} />
        </button>
      </div>

      {isOpen && createPortal(
        <div
          ref={(node) => { popupRef.current = node; refs.setFloating(node); }}
          className="bg-white rounded-2xl shadow-xl border border-border-white p-2"
          style={{ position: strategy, top: y ?? 0, left: x ?? 0, zIndex: 9999 }}
        >
          <div className="flex gap-4">
            {!state.rrule && showRepeat ? (
              <DateEditor
                label="Start Date"
                state={state.start}
                onChange={(updates) => setState((prev) => ({ ...prev, start: { ...prev.start, ...updates } }))}
                toggle={{
                  label: "Start Today",
                  value: isStartToday,
                  onChange: (v) => setState((prev) => ({
                    ...prev,
                    start: { ...prev.start, offset: v ? "P0D" : null, abs: v ? null : new Date() },
                  })),
                }}
              />
            ) : (
              <>
                <DateEditor
                  label="Start Date"
                  state={state.start}
                  onChange={(updates) => setState((prev) => ({ ...prev, start: { ...prev.start, ...updates } }))}
                  otherAbs={state.end.abs}
                  toggle={{
                    label: "Start Today",
                    value: isStartToday,
                    onChange: (v) => setState((prev) => ({
                      ...prev,
                      start: { ...prev.start, offset: v ? "P0D" : null, abs: v ? null : new Date() },
                    })),
                  }}
                />
                <div className="w-px bg-border-white" />
                <DateEditor
                  label="End Date"
                  state={state.end}
                  onChange={(updates) => setState((prev) => ({ ...prev, end: { ...prev.end, ...updates } }))}
                  otherAbs={state.start.abs}
                  isNoEnd={isNoEnd}
                  toggle={{
                    label: "No End",
                    value: isNoEnd,
                    onChange: (v) => setState((prev) => ({
                      ...prev,
                      end: { ...prev.end, abs: v ? null : new Date(), offset: null },
                    })),
                  }}
                />
              </>
            )}
            {showRepeat && (
              <>
                <div className="w-px bg-border-white" />
                <div className="p-2" style={{ width: state.rrule === "WEEKLY" ? "280px" : "160px" }}>
                  <div className="text-14 text-hover-black mb-3">Repeat</div>
                  {state.rrule && (
                    <div className="mb-3 flex items-center gap-2">
                      <span className="text-14 text-hover-black">Every</span>
                      <input
                        type="number"
                        min="1"
                        value={state.interval}
                        onChange={(e) => setState((prev) => ({ ...prev, interval: e.target.value }))}
                        onBlur={(e) => {
                          const num = parseInt(e.target.value, 10);
                          setState((prev) => ({
                            ...prev,
                            interval: !isNaN(num) && num > 0 && Number.isInteger(num) ? num.toString() : "1",
                          }));
                        }}
                        className="w-16 px-2 py-1 bg-white border border-border-white rounded-lg text-14 text-hover-black text-center"
                      />
                    </div>
                  )}
                  <div className="space-y-2">
                    {["", "DAILY", "WEEKLY", "MONTHLY", "YEARLY"].map((f) => (
                      <button
                        key={f}
                        onClick={() => setState((prev) => ({
                          ...prev,
                          rrule: f || null,
                          interval: f && !prev.rrule ? "1" : prev.interval,
                        }))}
                        className={`w-full px-3 py-2 rounded-lg text-14 text-left flex items-center gap-2 ${
                          state.rrule === (f || null)
                            ? "bg-brand-primary text-white"
                            : "bg-icon-hover-white text-hover-black hover:bg-sidebar-hover-white"
                        }`}
                      >
                        {getIcon(f, state.rrule === (f || null))}
                        {f === "" ? "None" : f.charAt(0) + f.slice(1).toLowerCase()}
                      </button>
                    ))}
                  </div>
                  {state.rrule === "WEEKLY" && (
                    <div className="mt-3 pt-3 border-t border-border-white">
                      <div className="text-xs text-hover-black mb-2">Repeat on</div>
                      <div className="grid grid-cols-7 gap-1">
                        {[
                          { short: "S", full: "SU" }, { short: "M", full: "MO" }, { short: "T", full: "TU" },
                          { short: "W", full: "WE" }, { short: "T", full: "TH" }, { short: "F", full: "FR" },
                          { short: "S", full: "SA" },
                        ].map(({ short, full }) => {
                          const isSelected = state.byWeekday.includes(full);
                          return (
                            <button
                              key={full}
                              onClick={() => setState((prev) => ({
                                ...prev,
                                byWeekday: isSelected
                                  ? prev.byWeekday.filter((d) => d !== full)
                                  : [...prev.byWeekday, full],
                              }))}
                              className={`w-8 h-8 rounded-full text-xs font-medium transition-colors ${
                                isSelected
                                  ? "bg-brand-primary text-white"
                                  : "bg-icon-hover-white text-hover-black hover:bg-sidebar-hover-white"
                              }`}
                            >
                              {short}
                            </button>
                          );
                        })}
                      </div>
                    </div>
                  )}
                  {state.rrule && (
                    <div className="mt-3 pt-3 border-t border-border-white">
                      <div className="flex items-center gap-2 mb-2">
                        <Clock className="w-3.5 h-3.5 text-brand-primary" />
                        <span className="text-xs text-hover-black">Time of day</span>
                      </div>
                      <div className="flex items-center gap-2">
                        <input
                          type="time"
                          value={state.byTime || ""}
                          onChange={(e) => setState((prev) => ({ ...prev, byTime: e.target.value || null }))}
                          className="px-2 py-1.5 bg-white border border-border-white rounded-lg text-14 text-hover-black w-full"
                        />
                        {state.byTime && (
                          <button
                            onClick={() => setState((prev) => ({ ...prev, byTime: null }))}
                            className="text-xs text-normal-black hover:text-hover-black whitespace-nowrap"
                          >
                            Clear
                          </button>
                        )}
                      </div>
                    </div>
                  )}
                </div>
              </>
            )}
          </div>
          <div className="flex justify-end mt-4 pt-4 border-t border-border-white px-2">
            <button
              onClick={handleApply}
              className="px-4 py-2 rounded-lg bg-brand-primary text-white text-14 hover:opacity-90"
            >
              Apply
            </button>
          </div>
        </div>,
        document.body,
      )}
    </div>
  );
};

export default DatePicker;
