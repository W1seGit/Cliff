"use client";

import React from "react";
import { Check } from "lucide-react";
import { Button } from "./button";

export interface WizardTabsProps {
  steps: string[];
  /** One icon per step. A finished step shows a check instead. */
  icons?: React.ReactNode[];
  currentStep: number;
  canVisitStep: (index: number) => boolean;
  stepValid: boolean[];
  onStepChange: (index: number) => void;
  ariaLabel?: string;
}

export function WizardTabs({
  steps,
  icons,
  currentStep,
  canVisitStep,
  stepValid,
  onStepChange,
  ariaLabel,
}: WizardTabsProps) {
  return (
    <div className="tabs" role="tablist" aria-label={ariaLabel}>
      {steps.map((label, index) => (
        <button
          key={label}
          type="button"
          role="tab"
          aria-selected={index === currentStep}
          disabled={!canVisitStep(index)}
          className={
            index === currentStep
              ? "active"
              : stepValid[index]
              ? "done"
              : ""
          }
          onClick={() => onStepChange(index)}
        >
          {index < currentStep && stepValid[index] ? <Check size={14} aria-hidden="true" /> : icons?.[index]}
          {label}
        </button>
      ))}
    </div>
  );
}

export interface WizardActionsProps {
  currentStep: number;
  totalSteps: number;
  busy: boolean;
  canContinue: boolean;
  canSubmit: boolean;
  submitLabel: string;
  submittingLabel?: string;
  onBack: () => void;
  onContinue: () => void;
  onSubmit: () => void;
}

export function WizardActions({
  currentStep,
  totalSteps,
  busy,
  canContinue,
  canSubmit,
  submitLabel,
  submittingLabel,
  onBack,
  onContinue,
  onSubmit,
}: WizardActionsProps) {
  const isLastStep = currentStep === totalSteps - 1;

  return (
    <div className="wizard-actions">
      <Button disabled={currentStep === 0 || busy} onClick={onBack}>
        Previous step
      </Button>
      {isLastStep ? (
        <Button
          variant="primary"
          disabled={!canSubmit || busy}
          onClick={onSubmit}
          loading={busy}
          loadingText={submittingLabel || "Working..."}
        >
          {submitLabel}
        </Button>
      ) : (
        <Button
          variant="primary"
          disabled={!canContinue || busy}
          onClick={onContinue}
        >
          Continue
        </Button>
      )}
    </div>
  );
}
