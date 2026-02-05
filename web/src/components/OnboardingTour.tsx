import { useState, useEffect, useCallback, type ReactNode } from "react";

const ONBOARDING_COMPLETE_KEY = "otter_camp_onboarding_complete";

type TourStep = {
  id: string;
  title: string;
  description: string;
  icon: string;
  targetSelector?: string;
  position?: "center" | "top" | "bottom" | "left" | "right";
};

const TOUR_STEPS: TourStep[] = [
  {
    id: "welcome",
    title: "Welcome to Otter Camp! 🦦",
    description:
      "Let's take a quick tour to help you get started. Otter Camp helps you manage tasks with the help of AI agents.",
    icon: "👋",
    position: "center",
  },
  {
    id: "create-task",
    title: "Create Your First Task",
    description:
      'Click the "+ New Task" button or use the command palette to quickly create tasks. Add details, assign agents, and set priorities.',
    icon: "✏️",
    targetSelector: '[data-tour="create-task"]',
    position: "bottom",
  },
  {
    id: "kanban-board",
    title: "Kanban Board",
    description:
      "Drag and drop tasks between columns to update their status. See your workflow at a glance with Todo, In Progress, and Done columns.",
    icon: "📋",
    targetSelector: '[data-tour="kanban-board"]',
    position: "top",
  },
  {
    id: "agent-chat",
    title: "Chat with Agents",
    description:
      "Each task has an AI agent ready to help. Open a task and use the chat to collaborate, get suggestions, and track progress.",
    icon: "🤖",
    targetSelector: '[data-tour="agent-chat"]',
    position: "left",
  },
  {
    id: "command-palette",
    title: "Command Palette",
    description:
      "Press ⌘K (or Ctrl+K) to open the command palette. Quickly search, create tasks, navigate, and perform actions without leaving the keyboard.",
    icon: "⌨️",
    targetSelector: '[data-tour="command-palette"]',
    position: "bottom",
  },
];

type SpotlightPosition = {
  top: number;
  left: number;
  width: number;
  height: number;
};

type TooltipPosition = {
  top?: number;
  bottom?: number;
  left?: number;
  right?: number;
  transform?: string;
};

function getTooltipPosition(
  spotlight: SpotlightPosition | null,
  position: TourStep["position"]
): TooltipPosition {
  if (!spotlight || position === "center") {
    return {
      top: window.innerHeight / 2,
      left: window.innerWidth / 2,
      transform: "translate(-50%, -50%)",
    };
  }

  const padding = 16;
  const tooltipWidth = 360;

  switch (position) {
    case "top":
      return {
        bottom: window.innerHeight - spotlight.top + padding,
        left: spotlight.left + spotlight.width / 2,
        transform: "translateX(-50%)",
      };
    case "bottom":
      return {
        top: spotlight.top + spotlight.height + padding,
        left: spotlight.left + spotlight.width / 2,
        transform: "translateX(-50%)",
      };
    case "left":
      return {
        top: spotlight.top + spotlight.height / 2,
        right: window.innerWidth - spotlight.left + padding,
        transform: "translateY(-50%)",
      };
    case "right":
      return {
        top: spotlight.top + spotlight.height / 2,
        left: spotlight.left + spotlight.width + padding,
        transform: "translateY(-50%)",
      };
    default:
      return {
        top: spotlight.top + spotlight.height + padding,
        left: Math.max(
          padding,
          Math.min(
            spotlight.left + spotlight.width / 2 - tooltipWidth / 2,
            window.innerWidth - tooltipWidth - padding
          )
        ),
      };
  }
}

type OnboardingTourProps = {
  children?: ReactNode;
  forceShow?: boolean;
};

export default function OnboardingTour({
  children,
  forceShow = false,
}: OnboardingTourProps) {
  const [isActive, setIsActive] = useState(false);
  const [currentStep, setCurrentStep] = useState(0);
  const [spotlightPosition, setSpotlightPosition] =
    useState<SpotlightPosition | null>(null);

  // Check if onboarding should show
  useEffect(() => {
    if (forceShow) {
      setIsActive(true);
      return;
    }

    const isComplete = localStorage.getItem(ONBOARDING_COMPLETE_KEY);
    if (!isComplete) {
      // Small delay to let the UI render first
      const timer = setTimeout(() => setIsActive(true), 500);
      return () => clearTimeout(timer);
    }
  }, [forceShow]);

  // Update spotlight position when step changes
  useEffect(() => {
    if (!isActive) return;

    const step = TOUR_STEPS[currentStep];
    if (!step.targetSelector) {
      setSpotlightPosition(null);
      return;
    }

    const updatePosition = () => {
      const element = document.querySelector(step.targetSelector!);
      if (element) {
        const rect = element.getBoundingClientRect();
        const padding = 8;
        setSpotlightPosition({
          top: rect.top - padding,
          left: rect.left - padding,
          width: rect.width + padding * 2,
          height: rect.height + padding * 2,
        });
      } else {
        setSpotlightPosition(null);
      }
    };

    updatePosition();
    window.addEventListener("resize", updatePosition);
    window.addEventListener("scroll", updatePosition, true);

    return () => {
      window.removeEventListener("resize", updatePosition);
      window.removeEventListener("scroll", updatePosition, true);
    };
  }, [isActive, currentStep]);

  const handleNext = useCallback(() => {
    if (currentStep < TOUR_STEPS.length - 1) {
      setCurrentStep((prev) => prev + 1);
    } else {
      handleComplete();
    }
  }, [currentStep]);

  const handleBack = useCallback(() => {
    if (currentStep > 0) {
      setCurrentStep((prev) => prev - 1);
    }
  }, [currentStep]);

  const handleSkip = useCallback(() => {
    handleComplete();
  }, []);

  const handleComplete = useCallback(() => {
    localStorage.setItem(ONBOARDING_COMPLETE_KEY, "true");
    setIsActive(false);
    setCurrentStep(0);
  }, []);

  // Handle keyboard navigation
  useEffect(() => {
    if (!isActive) return;

    const handleKeyDown = (e: KeyboardEvent) => {
      if (e.key === "Escape") {
        handleSkip();
      } else if (e.key === "ArrowRight" || e.key === "Enter") {
        handleNext();
      } else if (e.key === "ArrowLeft") {
        handleBack();
      }
    };

    window.addEventListener("keydown", handleKeyDown);
    return () => window.removeEventListener("keydown", handleKeyDown);
  }, [isActive, handleNext, handleBack, handleSkip]);

  if (!isActive) {
    return <>{children}</>;
  }

  const step = TOUR_STEPS[currentStep];
  const tooltipPosition = getTooltipPosition(spotlightPosition, step.position);
  const isFirstStep = currentStep === 0;
  const isLastStep = currentStep === TOUR_STEPS.length - 1;

  return (
    <>
      {children}

      {/* Overlay */}
      <div className="fixed inset-0 z-[9998]" aria-hidden="true">
        {/* Dark backdrop with spotlight cutout */}
        <svg className="h-full w-full">
          <defs>
            <mask id="spotlight-mask">
              <rect x="0" y="0" width="100%" height="100%" fill="white" />
              {spotlightPosition && (
                <rect
                  x={spotlightPosition.left}
                  y={spotlightPosition.top}
                  width={spotlightPosition.width}
                  height={spotlightPosition.height}
                  rx="12"
                  fill="black"
                />
              )}
            </mask>
          </defs>
          <rect
            x="0"
            y="0"
            width="100%"
            height="100%"
            fill="rgba(15, 23, 42, 0.75)"
            mask="url(#spotlight-mask)"
          />
        </svg>

        {/* Spotlight border glow */}
        {spotlightPosition && (
          <div
            className="pointer-events-none absolute rounded-xl ring-4 ring-otter-dark-accent/50 ring-offset-2 ring-offset-transparent transition-all duration-300"
            style={{
              top: spotlightPosition.top,
              left: spotlightPosition.left,
              width: spotlightPosition.width,
              height: spotlightPosition.height,
            }}
          />
        )}
      </div>

      {/* Tooltip */}
      <div
        className="fixed z-[9999] w-[360px] max-w-[calc(100vw-32px)] animate-fade-in"
        style={tooltipPosition}
        role="dialog"
        aria-modal="true"
        aria-labelledby="onboarding-title"
        aria-describedby="onboarding-description"
      >
        <div className="overflow-hidden rounded-2xl border border-[var(--border)] bg-[var(--surface)] shadow-2xl">
          {/* Header */}
          <div className="bg-otter-dark-accent px-6 py-4">
            <div className="flex items-center gap-3">
              <span className="text-3xl">{step.icon}</span>
              <div>
                <h3
                  id="onboarding-title"
                  className="text-lg font-semibold text-white"
                >
                  {step.title}
                </h3>
                <p className="text-sm text-otter-dark-bg/80">
                  Step {currentStep + 1} of {TOUR_STEPS.length}
                </p>
              </div>
            </div>
          </div>

          {/* Content */}
          <div className="px-6 py-4">
            <p
              id="onboarding-description"
              className="text-sm leading-relaxed text-slate-600 dark:text-slate-300"
            >
              {step.description}
            </p>
          </div>

          {/* Progress dots */}
          <div className="flex justify-center gap-1.5 pb-3">
            {TOUR_STEPS.map((_, index) => (
              <button
                key={index}
                type="button"
                onClick={() => setCurrentStep(index)}
                className={`h-2 rounded-full transition-all ${
                  index === currentStep
                    ? "w-6 bg-[var(--accent)]"
                    : "w-2 bg-[var(--surface-alt)] hover:bg-[var(--border)]"
                }`}
                aria-label={`Go to step ${index + 1}`}
              />
            ))}
          </div>

          {/* Footer */}
          <div className="flex items-center justify-between border-t border-[var(--border)] bg-[var(--surface-alt)] px-6 py-3">
            <button
              type="button"
              onClick={handleSkip}
              className="text-sm font-medium text-[var(--text-muted)] transition hover:text-[var(--text)]"
            >
              Skip tour
            </button>

            <div className="flex gap-2">
              {!isFirstStep && (
                <button
                  type="button"
                  onClick={handleBack}
                  className="btn-secondary rounded-lg px-4 py-2 text-sm font-medium transition"
                >
                  Back
                </button>
              )}
              <button
                type="button"
                onClick={handleNext}
                className="btn-primary rounded-lg px-4 py-2 text-sm font-medium shadow-sm transition"
              >
                {isLastStep ? "Get started" : "Next"}
              </button>
            </div>
          </div>
        </div>
      </div>
    </>
  );
}

// Helper to reset onboarding (for testing or re-showing)
export function resetOnboarding(): void {
  localStorage.removeItem(ONBOARDING_COMPLETE_KEY);
}

// Helper to check if onboarding is complete
export function isOnboardingComplete(): boolean {
  return localStorage.getItem(ONBOARDING_COMPLETE_KEY) === "true";
}
