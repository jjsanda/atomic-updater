// Horizontal bar list: one series, one color; every row carries its label
// and exact value as text, so nothing depends on color or hover.

interface Props {
  items: Record<string, number>;
  emptyText: string;
}

export default function BarList({ items, emptyText }: Props) {
  const rows = Object.entries(items).sort((a, b) => b[1] - a[1] || a[0].localeCompare(b[0]));
  if (rows.length === 0) {
    return <div className="empty">{emptyText}</div>;
  }
  const max = Math.max(...rows.map(([, count]) => count), 1);

  return (
    <div className="barlist">
      {rows.map(([label, count]) => (
        <div key={label} style={{ display: 'contents' }}>
          <div className="blabel mono" title={label}>
            {label}
          </div>
          <div className="btrack">
            <div className="bfill" style={{ width: `${(count / max) * 100}%` }} />
          </div>
          <div className="bvalue">{count.toLocaleString()}</div>
        </div>
      ))}
    </div>
  );
}
