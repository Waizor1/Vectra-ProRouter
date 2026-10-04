// Markup inside translated sentences, and a node's name as words with its flag.

import type { ComponentChildren } from 'preact';
import type { Named } from '../lib/names';

/** Stands for markup in a translated sentence; `around` puts the markup back in its place. */
export const SLOT = '\u0001';

/**
 * A translated sentence with markup where its placeholders were, whatever the
 * language's word order around them; several go back in the order they stand.
 */
export function around(text: string, ...inner: ComponentChildren[]) {
  const out: ComponentChildren[] = [];
  text.split(SLOT).forEach((part, i) => {
    out.push(part);
    if (i < inner.length) out.push(inner[i]);
  });
  return <>{out}</>;
}

/** A node's name: "Мост → 🇵🇱 Польша"; in a list of its own kind, `short`: "🇵🇱 Польша". The flag is decoration. */
export function Nm({ n, short }: { n: Named; short?: boolean }) {
  const flag = n.flag ? (
    <span class="fl fl-i" aria-hidden="true">
      {n.flag}
    </span>
  ) : null;
  return n.pre && n.short !== n.pre && !short ? (
    <>
      {n.pre}
      <span aria-hidden="true"> → </span>
      {flag}
      {n.short}
    </>
  ) : (
    <>
      {flag}
      {short ? n.short : n.name}
    </>
  );
}
