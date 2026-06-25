"use client";

import React, { useEffect, useRef, useState } from "react";
import { Search, SlidersHorizontal, X } from "lucide-react";
import { Input } from "./input";

export interface FilterOption {
  value: string;
  label: string;
}

export interface FilterField {
  key: string;
  label: string;
  type?: "text" | "select";
  placeholder?: string;
  value: string;
  onChange: (value: string) => void;
  options?: FilterOption[];
  multi?: boolean;
}

export interface FilterBarProps {
  fields: FilterField[];
  actions?: React.ReactNode;
  className?: string;
}

export function FilterBar({ fields, actions, className = "" }: FilterBarProps) {
  const classes = `filter-bar ${className}`.trim();
  const searchFields = fields.filter((f) => f.type !== "select");
  const filterFields = fields.filter((f) => f.type === "select");
  const [filtersOpen, setFiltersOpen] = useState(false);
  const filterRef = useRef<HTMLDivElement>(null);

  const activeFilters = filterFields.filter((f) => {
    if (f.multi) {
      const selected = f.value ? f.value.split(",") : [];
      return selected.length > 0;
    }
    const defaultVal = f.options?.[0]?.value;
    return f.value !== defaultVal;
  });

  useEffect(() => {
    if (!filtersOpen) return;
    function handleClickOutside(event: MouseEvent) {
      if (filterRef.current && !filterRef.current.contains(event.target as Node)) {
        setFiltersOpen(false);
      }
    }
    document.addEventListener("mousedown", handleClickOutside);
    return () => document.removeEventListener("mousedown", handleClickOutside);
  }, [filtersOpen]);

  function toggleMulti(field: FilterField, optionValue: string) {
    const current = field.value ? field.value.split(",") : [];
    const next = current.includes(optionValue)
      ? current.filter((v) => v !== optionValue)
      : [...current, optionValue];
    field.onChange(next.join(","));
  }

  return (
    <div className={classes}>
      <div className="filter-bar-row">
        {searchFields.map((field) => (
          <div key={field.key} className="filter-search">
            <Search size={16} className="filter-search-icon" />
            <Input
              placeholder={field.placeholder}
              value={field.value}
              onChange={(event) => field.onChange(event.target.value)}
            />
          </div>
        ))}

        {filterFields.length > 0 && (
          <div className="filter-menu-wrap" ref={filterRef}>
            <button
              type="button"
              className={`filter-menu-toggle ${activeFilters.length > 0 ? "has-active" : ""}`}
              onClick={() => setFiltersOpen((v) => !v)}
            >
              <SlidersHorizontal size={14} />
              Filters
              {activeFilters.length > 0 && <span className="filter-menu-badge">{activeFilters.length}</span>}
            </button>

            {filtersOpen && (
              <div className="filter-menu">
                {filterFields.map((field) => {
                  const options = field.options ?? [];
                  const defaultVal = options[0]?.value ?? "";
                  const visibleOptions = options.filter((o) => o.value !== defaultVal);
                  const selectedValues = field.multi ? (field.value ? field.value.split(",") : []) : [field.value];
                  return (
                    <div key={field.key} className="filter-menu-group">
                      <span className="filter-menu-title">{field.label}</span>
                      <div className="filter-menu-pills">
                        {visibleOptions.map((option) => {
                          const isSelected = selectedValues.includes(option.value);
                          return (
                            <button
                              key={option.value}
                              type="button"
                              className={`filter-menu-pill ${isSelected ? "selected" : ""}`}
                              onClick={() => {
                                if (field.multi) {
                                  toggleMulti(field, option.value);
                                } else {
                                  field.onChange(option.value === field.value ? defaultVal : option.value);
                                }
                              }}
                            >
                              {option.label}
                            </button>
                          );
                        })}
                      </div>
                    </div>
                  );
                })}
              </div>
            )}
          </div>
        )}

        {actions && <div className="filter-bar-actions">{actions}</div>}
      </div>

      {activeFilters.length > 0 && (
        <div className="filter-active-chips">
          {activeFilters.map((field) => {
            const defaultVal = field.options?.[0]?.value ?? "";
            if (field.multi) {
              const selected = field.value ? field.value.split(",") : [];
              return selected.map((val) => {
                const opt = field.options?.find((o) => o.value === val);
                return (
                  <button
                    key={`${field.key}-${val}`}
                    type="button"
                    className="filter-active-chip"
                    onClick={() => toggleMulti(field, val)}
                  >
                    {field.label}: {opt?.label ?? val}
                    <X size={12} />
                  </button>
                );
              });
            }
            const currentOption = field.options?.find((o) => o.value === field.value);
            return (
              <button
                key={field.key}
                type="button"
                className="filter-active-chip"
                onClick={() => field.onChange(defaultVal)}
              >
                {field.label}: {currentOption?.label ?? field.value}
                <X size={12} />
              </button>
            );
          })}
        </div>
      )}
    </div>
  );
}
