import { useEffect, useRef, useCallback, type RefObject } from "react";

/**
 * Focusable element selector for focus trap
 * Includes all standard focusable elements that are not disabled or hidden
 */
const FOCUSABLE_SELECTOR = [
  'a[href]:not([disabled]):not([tabindex="-1"])',
  'button:not([disabled]):not([tabindex="-1"])',
  'input:not([disabled]):not([tabindex="-1"])',
  'select:not([disabled]):not([tabindex="-1"])',
  'textarea:not([disabled]):not([tabindex="-1"])',
  '[tabindex]:not([tabindex="-1"]):not([disabled])',
  '[contenteditable="true"]:not([disabled])',
].join(", ");

type UseFocusTrapOptions = {
  /** Whether the focus trap is currently active */
  isActive: boolean;
  /** Callback when user presses Escape */
  onEscape?: () => void;
  /** Whether to return focus to trigger element on close */
  returnFocusOnClose?: boolean;
  /** Element to focus when trap activates (defaults to first focusable) */
  initialFocusRef?: RefObject<HTMLElement>;
  /** Element to return focus to on close (defaults to previously focused) */
  returnFocusRef?: RefObject<HTMLElement>;
};

type UseFocusTrapReturn = {
  /** Ref to attach to the container element */
  containerRef: RefObject<HTMLDivElement>;
  /** Get all focusable elements in the container */
  getFocusableElements: () => HTMLElement[];
  /** Focus the first focusable element */
  focusFirst: () => void;
  /** Focus the last focusable element */
  focusLast: () => void;
};

/**
 * useFocusTrap - Trap focus within a container for modal dialogs
 *
 * Implements WCAG 2.1 focus management requirements for modal dialogs:
 * - Traps Tab/Shift+Tab within the container
 * - Handles Escape key to close
 * - Returns focus to trigger element on close
 * - Supports custom initial focus element
 *
 * @example
 * ```tsx
 * function Modal({ isOpen, onClose }) {
 *   const { containerRef } = useFocusTrap({
 *     isActive: isOpen,
 *     onEscape: onClose,
 *     returnFocusOnClose: true,
 *   });
 *
 *   return (
 *     <div ref={containerRef} role="dialog" aria-modal="true">
 *       ...
 *     </div>
 *   );
 * }
 * ```
 *
 * @see https://www.w3.org/WAI/ARIA/apg/patterns/dialog-modal/
 */
export function useFocusTrap({
  isActive,
  onEscape,
  returnFocusOnClose = true,
  initialFocusRef,
  returnFocusRef,
}: UseFocusTrapOptions): UseFocusTrapReturn {
  const containerRef = useRef<HTMLDivElement>(null);
  const previousActiveElement = useRef<HTMLElement | null>(null);

  /**
   * Get all focusable elements within the container
   */
  const getFocusableElements = useCallback((): HTMLElement[] => {
    if (!containerRef.current) return [];

    const elements = containerRef.current.querySelectorAll<HTMLElement>(FOCUSABLE_SELECTOR);
    return Array.from(elements).filter((el) => {
      // Filter out elements that are visually hidden
      const style = window.getComputedStyle(el);
      return style.display !== "none" && style.visibility !== "hidden";
    });
  }, []);

  /**
   * Focus the first focusable element in the container
   */
  const focusFirst = useCallback(() => {
    const elements = getFocusableElements();
    elements[0]?.focus();
  }, [getFocusableElements]);

  /**
   * Focus the last focusable element in the container
   */
  const focusLast = useCallback(() => {
    const elements = getFocusableElements();
    elements[elements.length - 1]?.focus();
  }, [getFocusableElements]);

  // Store the previously focused element when trap activates
  useEffect(() => {
    if (isActive) {
      previousActiveElement.current = document.activeElement as HTMLElement;
    }
  }, [isActive]);

  // Handle initial focus when trap activates
  useEffect(() => {
    if (!isActive) return;

    // Use requestAnimationFrame to ensure DOM is ready
    const frameId = requestAnimationFrame(() => {
      if (initialFocusRef?.current) {
        initialFocusRef.current.focus();
      } else {
        focusFirst();
      }
    });

    return () => cancelAnimationFrame(frameId);
  }, [isActive, initialFocusRef, focusFirst]);

  // Return focus when trap deactivates
  useEffect(() => {
    if (isActive || !returnFocusOnClose) return;

    const elementToFocus = returnFocusRef?.current ?? previousActiveElement.current;
    
    if (elementToFocus && document.body.contains(elementToFocus)) {
      // Use requestAnimationFrame for smooth focus return
      requestAnimationFrame(() => {
        elementToFocus.focus();
      });
    }
  }, [isActive, returnFocusOnClose, returnFocusRef]);

  // Handle keyboard events for focus trapping
  useEffect(() => {
    if (!isActive) return;

    const handleKeyDown = (event: KeyboardEvent) => {
      // Handle Escape
      if (event.key === "Escape" && onEscape) {
        event.preventDefault();
        event.stopPropagation();
        onEscape();
        return;
      }

      // Handle Tab
      if (event.key !== "Tab") return;

      const focusableElements = getFocusableElements();
      if (focusableElements.length === 0) return;

      const firstElement = focusableElements[0];
      const lastElement = focusableElements[focusableElements.length - 1];
      const activeElement = document.activeElement;

      // Shift+Tab from first element -> focus last
      if (event.shiftKey && activeElement === firstElement) {
        event.preventDefault();
        lastElement.focus();
        return;
      }

      // Tab from last element -> focus first
      if (!event.shiftKey && activeElement === lastElement) {
        event.preventDefault();
        firstElement.focus();
        return;
      }

      // If focus is outside the container, bring it back
      if (!containerRef.current?.contains(activeElement)) {
        event.preventDefault();
        if (event.shiftKey) {
          lastElement.focus();
        } else {
          firstElement.focus();
        }
      }
    };

    document.addEventListener("keydown", handleKeyDown, true);
    return () => document.removeEventListener("keydown", handleKeyDown, true);
  }, [isActive, onEscape, getFocusableElements]);

  // Prevent focus from leaving the container via click
  useEffect(() => {
    if (!isActive) return;

    const handleFocusIn = (event: FocusEvent) => {
      if (
        containerRef.current &&
        !containerRef.current.contains(event.target as Node)
      ) {
        // Focus escaped - bring it back
        event.stopPropagation();
        focusFirst();
      }
    };

    document.addEventListener("focusin", handleFocusIn);
    return () => document.removeEventListener("focusin", handleFocusIn);
  }, [isActive, focusFirst]);

  return {
    containerRef,
    getFocusableElements,
    focusFirst,
    focusLast,
  };
}

export default useFocusTrap;
