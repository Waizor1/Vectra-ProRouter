// Per-browser UI preferences. Storage can be missing or throw (private mode,
// blocked site data, a sandboxed frame) — the UI must work the same without it.

const PREFIX = 'vectra.ui.';

export function load(key: string): string | null {
  try {
    return window.localStorage.getItem(PREFIX + key);
  } catch {
    return null;
  }
}

export function save(key: string, value: string): void {
  try {
    window.localStorage.setItem(PREFIX + key, value);
  } catch {
    /* preferences are a convenience; losing one is harmless */
  }
}

export function loadOneOf<T extends string>(key: string, allowed: readonly T[], fallback: T): T {
  const v = load(key);
  return v !== null && (allowed as readonly string[]).indexOf(v) >= 0 ? (v as T) : fallback;
}

/** Copy text; falls back to a hidden textarea where the async clipboard API is absent (plain-HTTP LuCI). */
export async function copyText(text: string, root: Node): Promise<boolean> {
  try {
    if (navigator.clipboard && window.isSecureContext) {
      await navigator.clipboard.writeText(text);
      return true;
    }
  } catch {
    /* denied or unavailable: the textarea path below still works */
  }
  const ta = document.createElement('textarea');
  ta.value = text;
  ta.setAttribute('readonly', '');
  ta.style.cssText = 'position:fixed;top:0;left:0;width:1px;height:1px;opacity:0;';
  root.appendChild(ta);
  try {
    ta.select();
    return document.execCommand('copy');
  } catch {
    return false;
  } finally {
    ta.remove();
  }
}
