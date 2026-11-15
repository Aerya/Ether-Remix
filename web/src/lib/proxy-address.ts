/** Import a standard authenticated proxy URL without keeping secrets in its address. */
export function importProxyAddress(
  raw: string,
): { url: string; username: string; password: string } | null {
  const text = raw.trim();
  if (!text.includes("@")) return null;
  try {
    if (/[\u0000-\u0020\u007f]/.test(text)) throw new Error();
    const parsed = new URL(text);
    if (
      !["http:", "https:", "socks5:"].includes(parsed.protocol) ||
      !parsed.hostname ||
      parsed.search ||
      parsed.hash ||
      (parsed.pathname && parsed.pathname !== "/")
    )
      throw new Error();
    const authority = text.match(/^[a-z][a-z0-9+.-]*:\/\/([^/?#]+)/i)?.[1];
    if (!authority || !authority.includes("@")) throw new Error();
    // Keep explicit default ports: URL.port normalizes :80/:443 away, but the backend requires a port.
    const host = authority.slice(authority.lastIndexOf("@") + 1);
    const port = host.match(/:(\d+)$/)?.[1];
    if (!port || Number(port) < 1 || Number(port) > 65535) throw new Error();
    const username = decodeURIComponent(parsed.username);
    const password = decodeURIComponent(parsed.password);
    if (
      /\s|[\u0000-\u001f\u007f]/.test(username) ||
      /[\u0000-\u001f\u007f]/.test(password) ||
      username.length > 256 ||
      password.length > 256
    )
      throw new Error();
    return { url: `${parsed.protocol}//${host}`, username, password };
  } catch {
    // Never surface the parser exception: it may contain credentials.
    throw new Error("Invalid authenticated proxy URL");
  }
}
