import { useEffect } from 'react';
import { isDemoMode } from '../lib/demo';

/**
 * Demo Mode Banner
 * Shows when running on demo.otter.camp
 * 
 * Design spec from Jeff G:
 * - Orange background (#C87941)
 * - White text, centered
 * - Subtle but clear indication this is sample data
 */

interface DemoBannerProps {
  className?: string;
}

export default function DemoBanner({ className = '' }: DemoBannerProps) {
  const isDemo = isDemoMode();

  // Add body class for layout offset when demo banner is shown
  useEffect(() => {
    if (isDemo) {
      document.body.classList.add('has-demo-banner');
    }
    return () => {
      document.body.classList.remove('has-demo-banner');
    };
  }, [isDemo]);

  if (!isDemo) return null;

  return (
    <div className={`demo-banner ${className}`}>
      <span className="demo-banner-icon">🦦</span>
      <span className="demo-banner-text">
        Demo Mode — This is sample data.{' '}
        <a href="https://otter.camp" className="demo-banner-link">
          Sign up for your own dashboard →
        </a>
      </span>
    </div>
  );
}
