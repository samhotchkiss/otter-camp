import { useState } from "react";

export default function OrgPickerPage() {
  const [orgId, setOrgId] = useState("");
  const [error, setError] = useState<string | null>(null);

  const handleSave = () => {
    const trimmed = orgId.trim();
    if (!trimmed) {
      setError("Org ID is required");
      return;
    }
    localStorage.setItem("otter-camp-org-id", trimmed);
    window.location.reload();
  };

  return (
    <div className="login-page">
      <div className="login-container">
        <div className="login-header">
          <div className="login-logo">🦦</div>
          <h1 className="login-title">Choose your organization</h1>
          <p className="login-subtitle">Enter the org ID to continue</p>
        </div>

        <div className="login-card">
          {error && <div className="login-error">{error}</div>}
          <div className="form-group">
            <label htmlFor="orgId" className="form-label">
              Organization ID
            </label>
            <input
              id="orgId"
              type="text"
              className="form-input"
              placeholder="a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11"
              value={orgId}
              onChange={(e) => setOrgId(e.target.value)}
            />
            <p className="form-hint">
              Ask your admin for the organization ID if you don’t know it.
            </p>
          </div>

          <button type="button" className="btn btn-primary" onClick={handleSave}>
            Continue
          </button>
        </div>
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
          margin-bottom: 32px;
        }

        .login-logo {
          font-size: 56px;
          margin-bottom: 12px;
        }

        .login-title {
          font-size: 24px;
          font-weight: 700;
          color: var(--text, #FAF8F5);
          margin-bottom: 6px;
        }

        .login-subtitle {
          font-size: 14px;
          color: var(--text-muted, #A69582);
        }

        .login-card {
          background: var(--surface, #252422);
          border: 1px solid var(--border, #3D3A36);
          border-radius: 16px;
          padding: 28px;
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
          padding: 12px 14px;
          background: var(--surface-2, #2D2A27);
          border: 1px solid var(--border, #3D3A36);
          border-radius: 12px;
          color: var(--text, #FAF8F5);
          font-size: 14px;
        }

        .form-hint {
          margin-top: 8px;
          font-size: 12px;
          color: var(--text-muted, #A69582);
        }

        .login-error {
          background: rgba(184, 92, 56, 0.15);
          border: 1px solid rgba(184, 92, 56, 0.3);
          color: #E57A52;
          padding: 12px 16px;
          border-radius: 10px;
          font-size: 14px;
          margin-bottom: 16px;
        }
      `}</style>
    </div>
  );
}
