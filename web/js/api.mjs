export async function fetchJSON(url, options) {
  const response = await fetch(url, options);
  const contentType = response.headers.get("Content-Type") || "";

  if (!response.ok) {
    let message = "";
    if (contentType.includes("application/json")) {
      const payload = await response.json().catch(() => null);
      message = payload?.error || payload?.message || "";
    } else {
      message = await response.text();
    }
    throw new Error(message || `请求失败：${response.status}`);
  }

  if (contentType.includes("application/json")) {
    return response.json();
  }
  return null;
}
