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
  rangeStart?: Date | null;
  rangeEnd?: Date | null;
  infiniteEnd?: boolean;
}

const monthNames = [
  "January", "February", "March", "April", "May", "June",
  "July", "August", "September", "October", "November", "December",
];

const dayNames = ["Su", "Mo", "Tu", "We", "Th", "Fr", "Sa"];

const Calendar: React.FC<CalendarProps> = ({
  selected,
  onSelect,
  rangeStart,
  rangeEnd,
  infiniteEnd = false,
}) => {
  const [currentDate, setCurrentDate] = useState<Date>(selected || new Date());

  useEffect(() => {
    if (selected) setCurrentDate(selected);
  }, [selected]);

  const today = new Date();
  const currentYear = currentDate.getFullYear();
  const currentMonth = currentDate.getMonth();

  const firstDayOfMonth = new Date(currentYear, currentMonth, 1);
  const lastDayOfMonth = new Date(currentYear, currentMonth + 1, 0);
  const firstDayWeekday = firstDayOfMonth.getDay();
  const daysInMonth = lastDayOfMonth.getDate();

  const previousMonth = new Date(currentYear, currentMonth - 1, 0);
  const daysInPreviousMonth = previousMonth.getDate();

  const calendarDays: CalendarDay[] = [];

  for (let i = firstDayWeekday - 1; i >= 0; i--) {
    calendarDays.push({ day: daysInPreviousMonth - i, isCurrentMonth: false, isPrevious: true });
  }
  for (let day = 1; day <= daysInMonth; day++) {
    calendarDays.push({ day, isCurrentMonth: true, isPrevious: false, isNext: false });
  }
  const remainingCells = (7 - (calendarDays.length % 7)) % 7;
  for (let day = 1; day <= remainingCells; day++) {
    calendarDays.push({ day, isCurrentMonth: false, isNext: true });
  }

  const isDateSelected = (day: number): boolean => {
    if (!selected || isNaN(selected.getTime())) return false;
    return selected.toDateString() === new Date(currentYear, currentMonth, day, 12, 0, 0).toDateString();
  };

  const isToday = (day: number): boolean => {
    return today.toDateString() === new Date(currentYear, currentMonth, day, 12, 0, 0).toDateString();
  };

  const isInRange = (date: Date): boolean => {
    if (!rangeStart || isNaN(rangeStart.getTime())) return false;
    if (infiniteEnd) return date > rangeStart;
    if (!rangeEnd || isNaN(rangeEnd.getTime())) return false;
    return date > rangeStart && date < rangeEnd;
  };

  const isRangeStart = (date: Date): boolean => !!rangeStart && date.toDateString() === rangeStart.toDateString();
  const isRangeEnd = (date: Date): boolean => !!rangeEnd && date.toDateString() === rangeEnd.toDateString();

  return (
    <div style={{ fontSize: "14px" }}>
      {/* Header */}
      <div className="flex items-center mt-3 mb-3">
        <button
          onClick={() => setCurrentDate(new Date(currentYear, currentMonth - 1, 1))}
          className="bg-transparent border-none text-lg cursor-pointer px-2 py-1 text-hover-black"
        >
          ‹
        </button>
        <div className="flex-1 text-center font-semibold text-hover-black">
          {monthNames[currentMonth]} {currentYear}
        </div>
        <button
          onClick={() => setCurrentDate(new Date(currentYear, currentMonth + 1, 1))}
          className="bg-transparent border-none text-lg cursor-pointer px-2 py-1 text-hover-black"
        >
          ›
        </button>
      </div>

      {/* Weekday headers */}
      <div className="grid grid-cols-7 gap-0.5 mb-2">
        {dayNames.map((day) => (
          <div key={day} className="text-center text-xs text-normal-black py-1.5">{day}</div>
        ))}
      </div>

      {/* Calendar grid */}
      <div className="grid grid-cols-7 gap-0.5">
        {calendarDays.map((dateObj, index) => {
          const actualDate = dateObj.isCurrentMonth
            ? new Date(currentYear, currentMonth, dateObj.day, 12, 0, 0)
            : dateObj.isPrevious
              ? new Date(currentYear, currentMonth - 1, dateObj.day, 12, 0, 0)
              : new Date(currentYear, currentMonth + 1, dateObj.day, 12, 0, 0);

          let inRange = false, start = false, end = false, selectedDay = false;
          try {
            inRange = isInRange(actualDate);
            start = dateObj.isCurrentMonth && isRangeStart(actualDate);
            end = dateObj.isCurrentMonth && isRangeEnd(actualDate);
            selectedDay = dateObj.isCurrentMonth ? isDateSelected(dateObj.day) : false;
          } catch { /* noop */ }

          const showRangeBg = inRange || start || end;
          const isRowStart = index % 7 === 0;
          const isRowEnd = index % 7 === 6;

          let rangeBorderRadius = "";
          if (showRangeBg) {
            if (isRowStart && isRowEnd) rangeBorderRadius = "rounded-xl";
            else if (isRowStart) rangeBorderRadius = "rounded-l-xl";
            else if (isRowEnd) rangeBorderRadius = "rounded-r-xl";
          }

          const isActive = selectedDay || start || end;

          return (
            <div key={index} className="relative flex items-center justify-center">
              {showRangeBg && (
                <div className={`absolute inset-0 bg-brand-primary/15 ${rangeBorderRadius}`} />
              )}
              <button
                onClick={() => dateObj.isCurrentMonth && onSelect?.(actualDate)}
                disabled={!dateObj.isCurrentMonth}
                style={{ width: 24, height: 24, padding: 0, lineHeight: 0, fontSize: 14 }}
                className={`relative z-10 border-none flex items-center justify-center cursor-pointer rounded-sm
                  ${isActive ? "bg-brand-primary text-white rounded-full" : ""}
                  ${isToday(dateObj.day) && dateObj.isCurrentMonth && !isActive ? "bg-icon-hover-white text-brand-primary" : ""}
                  ${!dateObj.isCurrentMonth ? "text-normal-black cursor-default bg-transparent" : ""}
                  ${!isActive && !isToday(dateObj.day) && dateObj.isCurrentMonth ? "bg-transparent text-hover-black" : ""}
                `}
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
