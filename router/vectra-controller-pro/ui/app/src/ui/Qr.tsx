import { useMemo } from 'preact/hooks';
import { qrMatrix, qrPath } from '../lib/qr';

/** A QR code as one SVG path — dark on white whatever the theme, as scanners expect. */
export function Qr({ text, label, px = 220 }: { text: string; label: string; px?: number }) {
  const q = useMemo(() => {
    try {
      return qrPath(qrMatrix(text, 'M'), 4);
    } catch {
      return null;
    }
  }, [text]);
  if (!q) return null;
  return (
    <svg class="qr" viewBox={`0 0 ${q.size} ${q.size}`} width={px} height={px} role="img" aria-label={label} shape-rendering="crispEdges">
      <rect width={q.size} height={q.size} fill="#fff" />
      <path d={q.d} fill="#0d1117" />
    </svg>
  );
}
