import { memo, type ReactNode } from "react";

/**
 * Card component props.
 */
export interface CardProps {
  children: ReactNode;
  className?: string;
}

/**
 * Card header props with icon, title, badge, and meta support.
 */
export interface CardHeaderProps {
  icon?: string;
  title: string;
  badge?: {
    text: string;
    variant?: "success" | "warning" | "error" | "info";
  };
  meta?: string;
  children?: ReactNode;
}

/**
 * Card body props.
 */
export interface CardBodyProps {
  children: ReactNode;
  className?: string;
}

/**
 * Card footer props.
 */
export interface CardFooterProps {
  children: ReactNode;
  className?: string;
}

/**
 * Get badge CSS classes based on variant.
 */
function getBadgeClassName(
  variant?: "success" | "warning" | "error" | "info"
): string {
  const base = "badge";
  if (!variant) return base;

  const variantClasses: Record<string, string> = {
    success: "badge-success",
    warning: "badge-warning",
    error: "badge-error",
    info: "badge-info",
  };

  return `${base} ${variantClasses[variant] || ""}`;
}

/**
 * Card Header sub-component.
 *
 * Displays an optional icon, title, badge, and meta text.
 * Additional children are rendered after the title row.
 *
 * @example
 * <Card.Header
 *   icon="📋"
 *   title="Tasks"
 *   badge={{ text: "Active", variant: "success" }}
 *   meta="Last updated 5 min ago"
 * />
 */
function CardHeader({
  icon,
  title,
  badge,
  meta,
  children,
}: CardHeaderProps): JSX.Element {
  return (
    <header className="card-header">
      <div className="card-title-row">
        {icon && <span className="card-icon">{icon}</span>}
        <h3 className="card-title">{title}</h3>
        {badge && (
          <span className={getBadgeClassName(badge.variant)}>{badge.text}</span>
        )}
      </div>
      {meta && <p className="card-meta">{meta}</p>}
      {children}
    </header>
  );
}

/**
 * Card Body sub-component.
 *
 * Container for the main card content with standard padding.
 *
 * @example
 * <Card.Body>
 *   <p>Card content goes here</p>
 * </Card.Body>
 */
function CardBody({ children, className }: CardBodyProps): JSX.Element {
  const classes = className ? `card-body ${className}` : "card-body";
  return <div className={classes}>{children}</div>;
}

/**
 * Card Footer sub-component.
 *
 * Container for card actions or supplementary info.
 *
 * @example
 * <Card.Footer>
 *   <button className="btn btn-primary">Save</button>
 * </Card.Footer>
 */
function CardFooter({ children, className }: CardFooterProps): JSX.Element {
  const classes = className ? `card-footer ${className}` : "card-footer";
  return <footer className={classes}>{children}</footer>;
}

/**
 * Card component following the OtterCamp design system.
 *
 * Uses compound component pattern with Card.Header, Card.Body, and Card.Footer
 * sub-components for flexible composition.
 *
 * Design tokens:
 * - 12px border-radius
 * - Surface background with subtle border
 * - Header/footer with surface-alt background
 * - 16-20px consistent padding
 *
 * @example
 * // Basic card
 * <Card>
 *   <Card.Header icon="📋" title="Tasks" />
 *   <Card.Body>
 *     <p>Task list here</p>
 *   </Card.Body>
 * </Card>
 *
 * @example
 * // Card with badge and footer
 * <Card>
 *   <Card.Header
 *     icon="🚀"
 *     title="Deployments"
 *     badge={{ text: "Live", variant: "success" }}
 *     meta="Production environment"
 *   />
 *   <Card.Body>
 *     <p>Deployment details</p>
 *   </Card.Body>
 *   <Card.Footer>
 *     <button className="btn btn-primary">Deploy</button>
 *   </Card.Footer>
 * </Card>
 */
function CardComponent({ children, className }: CardProps): JSX.Element {
  const classes = className ? `card ${className}` : "card";
  return <article className={classes}>{children}</article>;
}

// Memoize for performance
const MemoizedCard = memo(CardComponent);
const MemoizedCardHeader = memo(CardHeader);
const MemoizedCardBody = memo(CardBody);
const MemoizedCardFooter = memo(CardFooter);

// Create the compound component with sub-components attached
type CardWithSubComponents = typeof MemoizedCard & {
  Header: typeof MemoizedCardHeader;
  Body: typeof MemoizedCardBody;
  Footer: typeof MemoizedCardFooter;
};

export const Card = MemoizedCard as CardWithSubComponents;
Card.Header = MemoizedCardHeader;
Card.Body = MemoizedCardBody;
Card.Footer = MemoizedCardFooter;

// Default export for convenience
export default Card;
