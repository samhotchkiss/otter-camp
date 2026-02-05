/**
 * Demo Mode Banner
 * Shows when running on demo.otter.camp
 */

interface DemoBannerProps {
  className?: string;
}

export function DemoBanner({ className = '' }: DemoBannerProps) {
  return (
    <div className={`demo-banner ${className}`}>
      <span className="demo-banner-icon">🦦</span>
      <span className="demo-banner-text">
        Demo Mode — This is sample data.{' '}
        <a href="https://otter.camp" className="demo-banner-link">
          Sign up for your own dashboard!
        </a>
      </span>
    </div>
  );
}

export function isDemoMode(): boolean {
  if (typeof window === 'undefined') return false;
  return window.location.hostname === 'demo.otter.camp';
}
