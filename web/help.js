const helpState = {
  docs: [],
  currentSlug: "",
};

document.addEventListener("DOMContentLoaded", async () => {
  await bootstrapHelp();
});

async function bootstrapHelp() {
  const response = await fetchJSON("/api/docs");
  helpState.docs = response.docs || [];
  const params = new URLSearchParams(window.location.search);
  const requested = params.get("doc");
  const initialSlug = requested || helpState.docs[0]?.slug || "";
  renderNav();
  if (initialSlug) {
    await loadDoc(initialSlug);
  }
}

async function loadDoc(slug) {
  const response = await fetchJSON(`/api/docs/${slug}`);
  helpState.currentSlug = response.slug;
  const params = new URLSearchParams(window.location.search);
  params.set("doc", response.slug);
  window.history.replaceState({}, "", `${window.location.pathname}?${params.toString()}`);
  renderNav();
  renderDoc(response);
}

function renderNav() {
  const container = document.getElementById("docsNav");
  container.innerHTML = "";
  for (const doc of helpState.docs) {
    const link = document.createElement("a");
    link.href = `?doc=${encodeURIComponent(doc.slug)}`;
    link.className = `doc-link ${doc.slug === helpState.currentSlug ? "active" : ""}`;
    link.innerHTML = `${escapeHTML(doc.title)}<small>${escapeHTML(doc.path)}</small>`;
    link.addEventListener("click", async (event) => {
      event.preventDefault();
      await loadDoc(doc.slug);
    });
    container.appendChild(link);
  }
}

function renderDoc(doc) {
  document.getElementById("docTitle").textContent = doc.title;
  document.getElementById("docMeta").textContent = doc.path;
  document.getElementById("docContent").innerHTML = renderMarkdown(doc.content);
}

function renderMarkdown(source) {
  const lines = source.replaceAll("\r\n", "\n").split("\n");
  const blocks = [];
  let index = 0;

  while (index < lines.length) {
    const line = lines[index];
    const trimmed = line.trim();

    if (!trimmed) {
      index += 1;
      continue;
    }

    if (trimmed.startsWith("```")) {
      const language = trimmed.slice(3).trim();
      index += 1;
      const code = [];
      while (index < lines.length && !lines[index].trim().startsWith("```")) {
        code.push(lines[index]);
        index += 1;
      }
      index += 1;
      blocks.push(
        `<pre><code class="language-${escapeAttribute(language)}">${escapeHTML(code.join("\n"))}</code></pre>`,
      );
      continue;
    }

    const heading = trimmed.match(/^(#{1,3})\s+(.*)$/);
    if (heading) {
      const level = heading[1].length;
      blocks.push(`<h${level}>${renderInline(heading[2])}</h${level}>`);
      index += 1;
      continue;
    }

    if (trimmed.startsWith("> ")) {
      const quote = [];
      while (index < lines.length && lines[index].trim().startsWith("> ")) {
        quote.push(lines[index].trim().slice(2));
        index += 1;
      }
      blocks.push(`<blockquote>${renderInline(quote.join(" "))}</blockquote>`);
      continue;
    }

    if (/^- /.test(trimmed)) {
      const items = [];
      while (index < lines.length && /^- /.test(lines[index].trim())) {
        items.push(`<li>${renderInline(lines[index].trim().slice(2))}</li>`);
        index += 1;
      }
      blocks.push(`<ul>${items.join("")}</ul>`);
      continue;
    }

    if (/^\d+\. /.test(trimmed)) {
      const items = [];
      while (index < lines.length && /^\d+\. /.test(lines[index].trim())) {
        items.push(`<li>${renderInline(lines[index].trim().replace(/^\d+\. /, ""))}</li>`);
        index += 1;
      }
      blocks.push(`<ol>${items.join("")}</ol>`);
      continue;
    }

    const paragraph = [];
    while (index < lines.length) {
      const next = lines[index].trim();
      if (!next || next.startsWith("```") || next.startsWith("> ") || /^#{1,3}\s+/.test(next) || /^- /.test(next) || /^\d+\. /.test(next)) {
        break;
      }
      paragraph.push(next);
      index += 1;
    }
    blocks.push(`<p>${renderInline(paragraph.join(" "))}</p>`);
  }

  return blocks.join("");
}

function renderInline(value) {
  let html = escapeHTML(value);
  html = html.replace(/\[([^\]]+)\]\(([^)]+)\)/g, '<a href="$2">$1</a>');
  html = html.replace(/\*\*([^*]+)\*\*/g, "<strong>$1</strong>");
  html = html.replace(/`([^`]+)`/g, "<code>$1</code>");
  return html;
}

async function fetchJSON(url) {
  const response = await fetch(url);
  if (!response.ok) {
    throw new Error(await response.text());
  }
  return response.json();
}

function escapeHTML(value) {
  return String(value)
    .replaceAll("&", "&amp;")
    .replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;");
}

function escapeAttribute(value) {
  return escapeHTML(value).replaceAll('"', "&quot;");
}
