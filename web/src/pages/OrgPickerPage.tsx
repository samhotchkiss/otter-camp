import { useEffect, useState } from "react";
import { API_URL } from "../lib/api";

type Org = {
  id: string;
  name: string;
  slug: string;
};

export default function OrgPickerPage() {
  const [orgs, setOrgs] = useState<Org[]>([]);
  const [selected, setSelected] = useState<string>("");
  const [error, setError] = useState<string | null>(null);
  const [isLoading, setIsLoading] = useState(true);

  useEffect(() => {
    const loadOrgs = async () => {
      setIsLoading(true);
      setError(null);
      try {
        const token = localStorage.getItem("otter_camp_token");
        const res = await fetch(`${API_URL}/api/orgs`, {
          headers: {
            ...(token ? { Authorization: `Bearer ${token}` } : {}),
          },
        });
        if (!res.ok) {
          throw new Error("Failed to load orgs");
        }
        const data = await res.json();
        const list = data.orgs || [];
        setOrgs(list);
        if (list.length === 1) {
          setSelected(list[0].id);
        }
      } catch (err) {
        setError(err instanceof Error ? err.message : "Failed to load orgs");
      } finally {
        setIsLoading(false);
      }
    };

    loadOrgs();
  }, []);

  const handleSave = () => {
    const trimmed = selected.trim();
    if (!trimmed) {
      setError("Select an organization to continue");
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
          <p className="login-subtitle">Select the workspace you want to use</p>
        </div>

        <div className="login-card">
          {error && <div className="login-error">{error}</div>}
          {isLoading ? (
            <p className="form-hint">Loading organizations...</p>
          ) : (
            <div className="form-group">
              <label htmlFor="orgId" className="form-label">
                Organization
              </label>
              <select
                id="orgId"
                className="form-input"
                value={selected}
                onChange={(e) => setSelected(e.target.value)}
              >
                <option value="">Select an organization</option>
                {orgs.map((org) => (
                  <option key={org.id} value={org.id}>
                    {org.name}
                  </option>
                ))}
              </select>
            </div>
          )}

          <button type="button" className="btn btn-primary" onClick={handleSave} disabled={isLoading}>
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
