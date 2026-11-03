# API reference

The document below is the panel's own OpenAPI 3.0 spec, generated from the Go
types and handlers. It is a snapshot taken from the code; the live panel serves
the same document at `GET $BASE/openapi.json` and renders it at `GET $BASE/docs`.

<p><a href="../">← API guide</a></p>

Enter your panel's **API base URL** (from *Settings → API*; it ends with `/v1`) and
press **Use** — **Try it out** will then call that panel. The address stays in this browser only; the spec still comes from this page.

<div class="swagger-server">
  <label for="swagger-server">API base URL</label>
  <input id="swagger-server" type="url" spellcheck="false" placeholder="https://vpn.example.com/&lt;api-path&gt;/v1">
  <button id="swagger-apply" type="button">Use</button>
  <span id="swagger-server-hint" class="swagger-server__hint"></span>
</div>

<div id="swagger-ui"></div>
<link rel="stylesheet" href="../../swaggerui/swagger-ui.css">
<script src="../../swaggerui/swagger-ui-bundle.js"></script>
<script>
(function () {
  var DEFAULT = "https://vpn.example.com/abc123/v1";
  var STORAGE = "rospanel-swagger-server";
  var input = document.getElementById("swagger-server");
  var hint = document.getElementById("swagger-server-hint");
  var root = document.getElementById("swagger-ui");

  function render(base) {
    fetch("../../openapi.json")
      .then(function (r) { return r.json(); })
      .then(function (spec) {
        spec.servers = [{ url: base }];
        root.innerHTML = "";
        window.ui = SwaggerUIBundle({
          spec: spec,
          dom_id: "#swagger-ui",
          deepLinking: true,
          docExpansion: "list",
          filter: true,
          presets: [SwaggerUIBundle.presets.apis],
          layout: "BaseLayout",
        });
      });
  }

  function apply() {
    var base = input.value.trim().replace(/\/+$/, "");
    if (!/^https?:\/\//i.test(base)) {
      hint.textContent = "URL must start with http:// or https://";
      return;
    }
    try { localStorage.setItem(STORAGE, base); } catch (e) { /* private mode */ }
    hint.textContent = "Using " + base;
    render(base);
  }

  var saved = DEFAULT;
  try { saved = localStorage.getItem(STORAGE) || DEFAULT; } catch (e) { /* private mode */ }
  input.value = saved;
  document.getElementById("swagger-apply").addEventListener("click", apply);
  input.addEventListener("keydown", function (e) { if (e.key === "Enter") apply(); });
  render(saved);
})();
</script>
