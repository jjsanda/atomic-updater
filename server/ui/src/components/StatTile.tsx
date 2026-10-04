interface Props {
  label: string;
  value: number | string;
  hint?: string;
  hero?: boolean;
}

export default function StatTile({ label, value, hint, hero = false }: Props) {
  return (
    <div className={hero ? 'tile hero' : 'tile'}>
      <div className="label">{label}</div>
      <div className="value">{typeof value === 'number' ? value.toLocaleString() : value}</div>
      {hint ? <div className="hint">{hint}</div> : null}
    </div>
  );
}
