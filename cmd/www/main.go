package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
)

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "3000"
	}

	handler := newServerHandler("www", loadJoinConfigFromEnv())

	log.Printf("🦦 Otter Camp coming soon page on port %s", port)
	if err := http.ListenAndServe(":"+port, handler); err != nil {
		log.Fatalf("Server failed: %v", err)
	}
}

type joinConfig struct {
	InviteCodes map[string]struct{}
}

func loadJoinConfigFromEnv() joinConfig {
	codes := map[string]struct{}{}
	for _, raw := range strings.Split(os.Getenv("OTTER_JOIN_INVITE_CODES"), ",") {
		code := strings.ToLower(strings.TrimSpace(raw))
		if code == "" {
			continue
		}
		codes[code] = struct{}{}
	}

	return joinConfig{InviteCodes: codes}
}

func (c joinConfig) isValidInviteCode(code string) bool {
	normalized := strings.ToLower(strings.TrimSpace(code))
	if normalized == "" {
		return false
	}
	_, ok := c.InviteCodes[normalized]
	return ok
}

func newServerHandler(staticDir string, cfg joinConfig) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	})

	mux.HandleFunc("/join/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.NotFound(w, r)
			return
		}

		code := strings.TrimPrefix(r.URL.Path, "/join/")
		if code == "" || strings.Contains(code, "/") || !cfg.isValidInviteCode(code) {
			http.NotFound(w, r)
			return
		}

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = fmt.Fprint(w, renderJoinPage(code))
	})

	mux.Handle("/", http.FileServer(http.Dir(staticDir)))
	return mux
}

func renderJoinPage(code string) string {
	return fmt.Sprintf(`<!doctype html>
<html lang="en">
  <head>
    <meta charset="utf-8" />
    <meta name="viewport" content="width=device-width, initial-scale=1" />
    <title>Join Otter Camp</title>
    <style>
      :root { color-scheme: light; }
      body { font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif; margin: 2rem auto; max-width: 640px; padding: 0 1rem; line-height: 1.4; }
      h1 { margin-bottom: 0.25rem; }
      .hint { color: #4b5563; margin-bottom: 1rem; }
      form { display: grid; gap: 0.75rem; }
      label { font-weight: 600; display: grid; gap: 0.25rem; }
      input { border: 1px solid #d1d5db; border-radius: 8px; padding: 0.6rem 0.7rem; font-size: 1rem; }
      button { border: 0; border-radius: 8px; padding: 0.7rem 1rem; font-size: 1rem; cursor: pointer; background: #111827; color: #fff; }
      #error { color: #b91c1c; min-height: 1.2rem; }
      #success { display: none; border: 1px solid #d1d5db; border-radius: 10px; padding: 1rem; margin-top: 1rem; background: #f9fafb; }
      code, pre { font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, monospace; white-space: pre-wrap; word-break: break-word; }
      .row { display: flex; gap: 0.5rem; align-items: center; }
    </style>
  </head>
  <body>
    <h1>Join Otter Camp</h1>
    <p class="hint">Complete signup to create your hosted workspace.</p>
    <p data-invite-code=%q hidden></p>

    <form id="join-form" novalidate>
      <label>Name
        <input type="text" name="name" autocomplete="name" required />
      </label>
      <label>Email
        <input type="email" name="email" autocomplete="email" required />
      </label>
      <label>Organization Name
        <input type="text" name="organization_name" autocomplete="organization" required />
      </label>
      <label>Desired Subdomain
        <input type="text" name="subdomain" autocapitalize="off" spellcheck="false" required />
      </label>
      <button type="submit" id="submit">Create Workspace</button>
      <div id="error" role="alert"></div>
    </form>

    <section id="success" aria-live="polite">
      <h2>Workspace Created</h2>
      <p>Subdomain: <strong id="workspace-url"></strong></p>
      <p>Run this on the machine running OpenClaw:</p>
      <pre id="cli-command"></pre>
      <div class="row">
        <button type="button" id="copy-command">Copy Command</button>
        <span id="copy-status" class="hint"></span>
      </div>
    </section>

    <script>
      (function () {
        var bootstrapURL = %q;
        var inviteCode = %q;
        var form = document.getElementById("join-form");
        var errorEl = document.getElementById("error");
        var orgNameEl = form.elements.organization_name;
        var subdomainEl = form.elements.subdomain;
        var successEl = document.getElementById("success");
        var workspaceEl = document.getElementById("workspace-url");
        var commandEl = document.getElementById("cli-command");
        var copyBtn = document.getElementById("copy-command");
        var copyStatus = document.getElementById("copy-status");
        var slugPattern = /^[a-z0-9-]{3,32}$/;
        var subdomainTouched = false;

        function slugifyOrgName(value) {
          return (value || "")
            .toLowerCase()
            .replace(/[^a-z0-9]+/g, "-")
            .replace(/^-+|-+$/g, "")
            .slice(0, 32);
        }

        function isValidSubdomain(slug) {
          if (!slugPattern.test(slug)) return false;
          if (slug[0] === "-" || slug[slug.length - 1] === "-") return false;
          return true;
        }

        function buildInstallCommand(token, slug) {
          return "curl -sSL otter.camp/install | bash -s -- --token " + token + " --url https://" + slug + ".otter.camp";
        }

        orgNameEl.addEventListener("input", function () {
          if (subdomainTouched) return;
          subdomainEl.value = slugifyOrgName(orgNameEl.value);
        });

        subdomainEl.addEventListener("input", function () {
          subdomainTouched = true;
          subdomainEl.value = subdomainEl.value.toLowerCase().replace(/[^a-z0-9-]/g, "").slice(0, 32);
        });

        copyBtn.addEventListener("click", async function () {
          var command = commandEl.textContent || "";
          if (!command) return;
          copyStatus.textContent = "";
          try {
            if (navigator.clipboard && navigator.clipboard.writeText) {
              await navigator.clipboard.writeText(command);
            } else {
              var range = document.createRange();
              range.selectNode(commandEl);
              var selection = window.getSelection();
              selection.removeAllRanges();
              selection.addRange(range);
              document.execCommand("copy");
              selection.removeAllRanges();
            }
            copyStatus.textContent = "Copied.";
          } catch (err) {
            copyStatus.textContent = "Copy failed.";
          }
        });

        form.addEventListener("submit", async function (event) {
          event.preventDefault();
          errorEl.textContent = "";
          copyStatus.textContent = "";

          var name = (form.elements.name.value || "").trim();
          var email = (form.elements.email.value || "").trim().toLowerCase();
          var organizationName = (form.elements.organization_name.value || "").trim();
          var subdomain = (form.elements.subdomain.value || "").trim().toLowerCase();

          if (!name || !email || !organizationName || !subdomain) {
            errorEl.textContent = "All fields are required.";
            return;
          }
          if (!isValidSubdomain(subdomain)) {
            errorEl.textContent = "Subdomain must match ^[a-z0-9-]{3,32}$ and cannot start/end with '-'.";
            return;
          }

          var payload = {
            name: name,
            email: email,
            organization_name: organizationName,
            org_slug: subdomain,
            invite_code: inviteCode
          };

          try {
            var response = await fetch(bootstrapURL, {
              method: "POST",
              headers: { "Content-Type": "application/json" },
              body: JSON.stringify(payload)
            });
            var result = await response.json();
            if (!response.ok) {
              errorEl.textContent = (result && result.error) ? result.error : "Signup failed.";
              return;
            }

            var confirmedSlug = (result && result.org_slug) ? String(result.org_slug) : subdomain;
            var token = (result && result.token) ? String(result.token) : "";
            var command = buildInstallCommand(token, confirmedSlug);

            workspaceEl.textContent = confirmedSlug + ".otter.camp";
            commandEl.textContent = command;
            successEl.style.display = "block";
            form.style.display = "none";
          } catch (err) {
            errorEl.textContent = "Unable to reach signup service. Please try again.";
          }
        });
      })();
    </script>
  </body>
</html>`, code, joinBootstrapEndpoint(), code)
}

func joinBootstrapEndpoint() string {
	if endpoint := strings.TrimSpace(os.Getenv("OTTER_JOIN_BOOTSTRAP_URL")); endpoint != "" {
		return endpoint
	}
	return "https://api.otter.camp/api/onboarding/bootstrap"
}
