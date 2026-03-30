import React, { useState, useEffect } from "react";

export type CalendarDay = {
  day: number;
  isCurrentMonth: boolean;
  isPrevious?: boolean;
  isNext?: boolean;
};

interface CalendarProps {
  selected: Date | null;
  onSelect?: (date: Date) => void;
  mode?: string;
  rangeStart?: Date | null;
  rangeEnd?: Date | null;
  infiniteEnd?: boolean; // If true, highlights all dates after rangeStart
}

const monthNames = [
  "January",
  "February",
  "March",
  "April",
  "May",
  "June",
  "July",
  "August",
  "September",
  "October",
  "November",
  "December",
];

const dayNames = ["Su", "Mo", "Tu", "We", "Th", "Fr", "Sa"];

const Calendar: React.FC<CalendarProps> = ({
  selected,
  onSelect,
  mode = "single",
  rangeStart,
  rangeEnd,
  infiniteEnd = false,
}) => {
  const [currentDate, setCurrentDate] = useState<Date>(selected || new Date());

  useEffect(() => {
    if (selected) {
      setCurrentDate(selected);
    }
  }, [selected]);

  const today = new Date();
  const currentYear = currentDate.getFullYear();
  const currentMonth = currentDate.getMonth();

  // Get first day of month and number of days
  const firstDayOfMonth = new Date(currentYear, currentMonth, 1);
  const lastDayOfMonth = new Date(currentYear, currentMonth + 1, 0);
  const firstDayWeekday = firstDayOfMonth.getDay();
  const daysInMonth = lastDayOfMonth.getDate();

  // Get previous month's last days
  const previousMonth = new Date(currentYear, currentMonth - 1, 0);
  const daysInPreviousMonth = previousMonth.getDate();

  // Generate calendar days
  const calendarDays: CalendarDay[] = [];

  // Previous month's trailing days
  for (let i = firstDayWeekday - 1; i >= 0; i--) {
    calendarDays.push({
      day: daysInPreviousMonth - i,
      isCurrentMonth: false,
      isPrevious: true,
    });
  }

  // Current month days
  for (let day = 1; day <= daysInMonth; day++) {
    calendarDays.push({
      day,
      isCurrentMonth: true,
      isPrevious: false,
      isNext: false,
    });
  }

  // Next month's leading days - only fill to complete the last week
  const remainingCells = (7 - (calendarDays.length % 7)) % 7;
  for (let day = 1; day <= remainingCells; day++) {
    calendarDays.push({
      day,
      isCurrentMonth: false,
      isNext: true,
    });
  }

  // Helpers
  const isDateSelected = (day: number): boolean => {
    if (!selected || !(selected instanceof Date) || isNaN(selected.getTime()))
      return false;
    const date = new Date(currentYear, currentMonth, day, 12, 0, 0);
    if (!(date instanceof Date) || isNaN(date.getTime())) return false;
    return selected.toDateString() === date.toDateString();
  };

  const isToday = (day: number): boolean => {
    if (!today || !(today instanceof Date) || isNaN(today.getTime()))
      return false;
    const date = new Date(currentYear, currentMonth, day, 12, 0, 0);
    if (!(date instanceof Date) || isNaN(date.getTime())) return false;
    return today.toDateString() === date.toDateString();
  };

  const isInRange = (date: Date | null): boolean => {
    if (!date || !(date instanceof Date) || isNaN(date.getTime())) return false;
    if (
      !rangeStart ||
      !(rangeStart instanceof Date) ||
      isNaN(rangeStart.getTime())
    )
      return false;

    // If infiniteEnd is true, highlight all dates after rangeStart
    if (infiniteEnd) {
      return date > rangeStart;
    }

    if (!rangeEnd || !(rangeEnd instanceof Date) || isNaN(rangeEnd.getTime()))
      return false;
    return date > rangeStart && date < rangeEnd;
  };

  const isRangeStart = (date: Date | null): boolean => {
    if (!date || !(date instanceof Date) || isNaN(date.getTime())) return false;
    if (
      !rangeStart ||
      !(rangeStart instanceof Date) ||
      isNaN(rangeStart.getTime())
    )
      return false;
    return date.toDateString() === rangeStart.toDateString();
  };

  const isRangeEnd = (date: Date | null): boolean => {
    if (!date || !(date instanceof Date) || isNaN(date.getTime())) return false;
    if (!rangeEnd || !(rangeEnd instanceof Date) || isNaN(rangeEnd.getTime()))
      return false;
    return date.toDateString() === rangeEnd.toDateString();
  };

  function isRowStart(idx: number) {
    return idx % 7 === 0;
  }
  function isRowEnd(idx: number) {
    return idx % 7 === 6;
  }

  return (
    <div
      className="calendar-container calendar-exempt"
      style={{ fontSize: "14px", fontFamily: "IBM Plex Mono, monospace" }}
    >
      {/* Header with navigation */}
      <div
        className="calendar-header"
        style={{
          display: "flex",
          alignItems: "center",
          marginTop: "12px",
          marginBottom: "12px",
        }}
      >
        <button
          onClick={() =>
            setCurrentDate(new Date(currentYear, currentMonth - 1, 1))
          }
          style={{
            background: "none",
            border: "none",
            fontSize: "18px",
            cursor: "pointer",
            padding: "4px 8px",
            display: "flex",
            alignItems: "center",
            justifyContent: "center",
            lineHeight: "0",
            color: "var(--color-text-hover-black)",
          }}
        >
          ‹
        </button>

        <div className="text-18b text-center flex-1 text-hover-black">
          {monthNames[currentMonth]} {currentYear}
        </div>

        <button
          onClick={() =>
            setCurrentDate(new Date(currentYear, currentMonth + 1, 1))
          }
          style={{
            background: "none",
            border: "none",
            fontSize: "18px",
            cursor: "pointer",
            padding: "4px 8px",
            display: "flex",
            alignItems: "center",
            justifyContent: "center",
            lineHeight: "1",
            color: "var(--color-text-hover-black)",
          }}
        >
          ›
        </button>
      </div>

      {/* Weekday headers */}
      <div
        className="calendar-weekdays"
        style={{
          display: "grid",
          gridTemplateColumns: "repeat(7, 1fr)",
          gap: "2px",
          marginBottom: "8px",
        }}
      >
        {dayNames.map((day) => (
          <div
            key={day}
            style={{
              textAlign: "center",
              fontSize: "14px",
              color: "var(--color-text-light-black)",
              padding: "6px 0",
            }}
          >
            {day}
          </div>
        ))}
      </div>

      {/* Calendar grid */}
      <div
        className="calendar-grid"
        style={{
          display: "grid",
          gridTemplateColumns: "repeat(7, 1fr)",
          gap: "2px",
        }}
      >
        {calendarDays.map((dateObj, index) => {
          // Construct the actual date for this cell
          const actualDate = dateObj.isCurrentMonth
            ? new Date(currentYear, currentMonth, dateObj.day, 12, 0, 0)
            : dateObj.isPrevious
              ? new Date(currentYear, currentMonth - 1, dateObj.day, 12, 0, 0)
              : new Date(currentYear, currentMonth + 1, dateObj.day, 12, 0, 0);

          const validDate =
            actualDate &&
            actualDate instanceof Date &&
            !isNaN(actualDate.getTime());
          let inRange = false,
            start = false,
            end = false,
            selectedDay = false;
          try {
            inRange = validDate && isInRange(actualDate);
            start =
              validDate && dateObj.isCurrentMonth && isRangeStart(actualDate);
            end = validDate && dateObj.isCurrentMonth && isRangeEnd(actualDate);
            selectedDay =
              selected && dateObj.isCurrentMonth
                ? isDateSelected(dateObj.day)
                : false;
          } catch {
            inRange = start = end = selectedDay = false;
          }
          // Range highlight style - show background for inRange, start, and end
          let rangeBg = "";
          const showRangeBg = inRange || start || end;

          if (showRangeBg) {
            if (isRowStart(index) && isRowEnd(index)) {
              rangeBg =
                "border-radius: 10px; background: var(--color-purple-300);";
            } else if (isRowStart(index)) {
              rangeBg =
                "border-top-left-radius: 10px; border-bottom-left-radius: 10px; background: var(--color-purple-300);";
            } else if (isRowEnd(index)) {
              rangeBg =
                "border-top-right-radius: 10px; border-bottom-right-radius: 10px; background: var(--color-purple-300);";
            } else {
              rangeBg = "background: var(--color-purple-300);";
            }
          }

          return (
            <div
              key={index}
              style={{
                position: "relative",
                width: "100%",
                height: "100%",
                display: "flex",
                alignItems: "center",
                justifyContent: "center",
              }}
            >
              {showRangeBg && (
                <div
                  style={{
                    position: "absolute",
                    inset: 0,
                    zIndex: 0,
                    pointerEvents: "none",
                    ...(rangeBg && {
                      ...Object.fromEntries(
                        rangeBg
                          .split(";")
                          .filter(Boolean)
                          .map((s) => {
                            const [k, v] = s.split(":");
                            return [k.trim(), v.trim()];
                          }),
                      ),
                    }),
                  }}
                />
              )}
              <button
                onClick={() => {
                  if (dateObj.isCurrentMonth && onSelect) {
                    try {
                      onSelect(actualDate);
                    } catch {
                      // Error in onSelect
                    }
                  }
                }}
                disabled={!dateObj.isCurrentMonth}
                style={{
                  width: "24px",
                  height: "24px",
                  border: "none",
                  background: selectedDay
                    ? "var(--color-purple-300)"
                    : isToday(dateObj.day) && dateObj.isCurrentMonth
                      ? "var(--color-icon-hover-white)"
                      : "transparent",
                  color:
                    start || end || inRange || selectedDay
                      ? "white"
                      : !dateObj.isCurrentMonth
                        ? "var(--color-text-light-black)"
                        : isToday(dateObj.day)
                          ? "var(--color-purple-600)"
                          : "var(--color-text-hover-black)",
                  fontSize: "14px",
                  cursor: dateObj.isCurrentMonth ? "pointer" : "default",
                  display: "flex",
                  alignItems: "center",
                  justifyContent: "center",
                  fontFamily: "IBM Plex Mono, monospace",
                  borderRadius: selectedDay ? "50%" : "4px",
                  padding: "0",
                  lineHeight: "0",
                  position: "relative",
                  zIndex: 1,
                }}
              >
                {dateObj.day}
              </button>
            </div>
          );
        })}
      </div>
    </div>
  );
};

export default Calendar;
