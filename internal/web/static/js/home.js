// formatting shared by the activity and devices pages

export function mbps(bytesPerSec) {
  const bits = (Number(bytesPerSec) || 0) * 8;
  if (bits >= 1e9) return `${(bits / 1e9).toFixed(2)} Gbps`;
  if (bits >= 1e6) return `${(bits / 1e6).toFixed(bits >= 1e8 ? 0 : 1)} Mbps`;
  if (bits >= 1e3) return `${Math.round(bits / 1e3)} kbps`;
  return '0 kbps';
}

export function bytes(n) {
  n = Number(n) || 0;
  if (n >= 1e12) return `${(n / 1e12).toFixed(2)} TB`;
  if (n >= 1e9) return `${(n / 1e9).toFixed(n >= 1e10 ? 0 : 1)} GB`;
  if (n >= 1e6) return `${Math.round(n / 1e6)} MB`;
  if (n >= 1e3) return `${Math.round(n / 1e3)} KB`;
  return `${n} B`;
}

export function findingsFor(s, mac) {
  return (s?.devices || []).find((d) => d.mac === mac)?.findings || [];
}
