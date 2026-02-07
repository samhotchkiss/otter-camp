import { useState, type FormEvent } from "react";
import { useAuth } from "../contexts/AuthContext";

/**
 * LoginPage - Clean magic link auth
 * Design: Jeff G (matches dashboard-v5/login.html mockup)
 * 
 * Uses our gold (#C9A86C) palette, not teal/emerald.
 */
export default function LoginPage() {
  const { login } = useAuth();
  const [email, setEmail] = useState("");
  const [name, setName] = useState("");
  const [org, setOrg] = useState("");
  const [isSubmitting, setIsSubmitting] = useState(false);
  const [emailSent, setEmailSent] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const handleSubmit = async (e: FormEvent) => {
    e.preventDefault();
    setError(null);
    setIsSubmitting(true);

    try {
      await login(email, name, org);
      setEmailSent(true);
    } catch (err) {
      setError(err instanceof Error ? err.message : "Failed to send magic link");
    } finally {
      setIsSubmitting(false);
    }
  };

  return (
    <div className="login-page">
      <div className="login-container">
        {/* Header */}
        <div className="login-header">
          <div className="login-logo">🦦</div>
          <h1 className="login-title">otter.camp</h1>
          <p className="login-subtitle">Your AI team's home base</p>
        </div>

        {/* Login Card */}
        <div className="login-card">
          {!emailSent ? (
            <>
              <h2 className="login-card-title">Sign in to your account</h2>

              {error && (
                <div className="login-error">{error}</div>
              )}

              <form onSubmit={handleSubmit}>
                <div className="form-group">
                  <label htmlFor="name" className="form-label">
                    Name
                  </label>
                  <input
                    id="name"
                    type="text"
                    className="form-input"
                    placeholder="Your name"
                    value={name}
                    onChange={(e) => setName(e.target.value)}
                    autoComplete="name"
                    autoFocus
                  />
                </div>

                <div className="form-group">
                  <label htmlFor="org" className="form-label">
                    Organization
                  </label>
                  <input
                    id="org"
                    type="text"
                    className="form-input"
                    placeholder="Your org name"
                    value={org}
                    onChange={(e) => setOrg(e.target.value)}
                  />
                </div>

                <div className="form-group">
                  <label htmlFor="email" className="form-label">
                    Email address
                  </label>
                  <input
                    id="email"
                    type="email"
                    className="form-input"
                    placeholder="you@example.com"
                    value={email}
                    onChange={(e) => setEmail(e.target.value)}
                    required
                    autoComplete="email"
                  />
                  <p className="form-hint">
                    We'll generate a magic link to sign in
                  </p>
                </div>

                <button
                  type="submit"
                  className="btn btn-primary"
                  disabled={isSubmitting}
                >
                  {isSubmitting ? "Sending..." : "Generate Magic Link"}
                </button>
              </form>
            </>
          ) : (
            <div className="login-success">
              <div className="success-icon">✉️</div>
              <h2 className="success-title">Check your email</h2>
              <p className="success-text">
                We generated a magic link for <strong>{email}</strong>
              </p>
              <p className="success-hint">
                If you weren't redirected automatically, check the URL bar for the auth link.
              </p>
              <button
                type="button"
                className="btn btn-secondary"
                onClick={() => setEmailSent(false)}
              >
                Use a different email
              </button>
            </div>
          )}
        </div>

        {/* Footer */}
        <p className="login-footer">
          Cozy, collaborative, and ready for the next adventure.
        </p>
      </div>

      <style>{`
        .login-page {
          min-height: 100vh;
          display: flex;
          align-items: center;
          justify-content: center;
          padding: 24px;
          background: var(--bg, #1A1918);
        }

        .login-container {
          width: 100%;
          max-width: 420px;
        }

        .login-header {
          text-align: center;
          margin-bottom: 40px;
        }

        .login-logo {
          font-size: 64px;
          margin-bottom: 16px;
        }

        .login-title {
          font-size: 28px;
          font-weight: 700;
          color: var(--text, #FAF8F5);
          margin-bottom: 8px;
        }

        .login-subtitle {
          font-size: 16px;
          color: var(--text-muted, #A69582);
        }

        .login-card {
          background: var(--surface, #252422);
          border: 1px solid var(--border, #3D3A36);
          border-radius: 16px;
          padding: 32px;
        }

        .login-card-title {
          font-size: 18px;
          font-weight: 600;
          color: var(--text, #FAF8F5);
          margin-bottom: 24px;
          text-align: center;
        }

        .login-error {
          background: rgba(184, 92, 56, 0.15);
          border: 1px solid rgba(184, 92, 56, 0.3);
          color: #E57A52;
          padding: 12px 16px;
          border-radius: 10px;
          font-size: 14px;
          margin-bottom: 20px;
        }

        .form-group {
          margin-bottom: 20px;
        }

        .form-label {
          display: block;
          font-size: 14px;
          font-weight: 500;
          color: var(--text, #FAF8F5);
          margin-bottom: 8px;
        }

        .form-input {
          width: 100%;
          padding: 14px 16px;
          background: var(--surface-alt, #2D2B28);
          border: 1px solid var(--border, #3D3A36);
          border-radius: 10px;
          font-size: 15px;
          color: var(--text, #FAF8F5);
          transition: all 0.15s;
        }

        .form-input::placeholder {
          color: var(--text-muted, #A69582);
        }

        .form-input:focus {
          outline: none;
          border-color: var(--accent, #C9A86C);
          box-shadow: 0 0 0 3px rgba(201, 168, 108, 0.2);
        }

        .form-hint {
          font-size: 13px;
          color: var(--text-muted, #A69582);
          margin-top: 8px;
        }

        .btn {
          display: flex;
          align-items: center;
          justify-content: center;
          gap: 8px;
          width: 100%;
          padding: 14px 24px;
          border-radius: 10px;
          font-size: 15px;
          font-weight: 600;
          border: none;
          cursor: pointer;
          transition: all 0.15s;
        }

        .btn-primary {
          background: var(--accent, #C9A86C);
          color: var(--bg, #1A1918);
        }

        .btn-primary:hover {
          background: var(--accent-hover, #D4B87A);
        }

        .btn-primary:disabled {
          opacity: 0.6;
          cursor: not-allowed;
        }

        .btn-secondary {
          background: var(--surface-alt, #2D2B28);
          color: var(--text, #FAF8F5);
          border: 1px solid var(--border, #3D3A36);
          margin-top: 16px;
        }

        .btn-secondary:hover {
          background: var(--border, #3D3A36);
        }

        .btn-oauth {
          background: var(--surface, #252422);
          border: 1px solid var(--border, #3D3A36);
          color: var(--text, #FAF8F5);
        }

        .btn-oauth:hover {
          background: var(--surface-alt, #2D2B28);
        }

        .divider {
          display: flex;
          align-items: center;
          gap: 16px;
          margin: 24px 0;
          color: var(--text-muted, #A69582);
          font-size: 13px;
        }

        .divider::before,
        .divider::after {
          content: '';
          flex: 1;
          height: 1px;
          background: var(--border, #3D3A36);
        }

        .alt-login {
          display: flex;
          flex-direction: column;
          gap: 12px;
        }

        .login-success {
          text-align: center;
        }

        .success-icon {
          width: 80px;
          height: 80px;
          margin: 0 auto 24px;
          background: rgba(90, 122, 92, 0.15);
          border-radius: 50%;
          display: flex;
          align-items: center;
          justify-content: center;
          font-size: 40px;
        }

        .success-title {
          font-size: 20px;
          font-weight: 600;
          color: var(--text, #FAF8F5);
          margin-bottom: 8px;
        }

        .success-text {
          color: var(--text, #FAF8F5);
          margin-bottom: 8px;
        }

        .success-hint {
          font-size: 14px;
          color: var(--text-muted, #A69582);
          margin-bottom: 24px;
        }

        .login-footer {
          text-align: center;
          font-size: 13px;
          color: var(--text-muted, #A69582);
          margin-top: 32px;
        }
      `}</style>
    </div>
  );
}
