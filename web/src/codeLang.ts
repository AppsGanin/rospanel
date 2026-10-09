// codeLang picks the editor's language for a file. Apart from CodeEditor so a page
// can ask without loading the editor itself.
export type CodeLang = "js" | "json" | "sql" | "md" | "text";

export function langOf(path: string): CodeLang {
  if (path.endsWith(".js") || path.endsWith(".ts")) return "js";
  if (path.endsWith(".json")) return "json";
  if (path.endsWith(".sql")) return "sql";
  if (path.endsWith(".md")) return "md";
  return "text";
}
