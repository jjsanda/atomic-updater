import { useEffect, useMemo, useRef, useState } from 'react';
import type { MarkbadBucket } from '../types';
import { absoluteTime, hourLabel } from '../format';

// Hand-rolled SVG line+area chart for hourly event-count buckets.
// Single series: 2px line, 10% area wash, hairline solid gridlines,
// crosshair + tooltip on hover, and a table-view twin behind a toggle.

const HEIGHT = 180; // plot height
const AXIS_BAND = 22; // x-axis label band, included in the svg height
const PAD_LEFT = 34;
const PAD_RIGHT = 12;
const PAD_TOP = 10;

function niceMax(maxValue: number): number {
  if (maxValue <= 4) return 4;
  const raw = maxValue * 1.15;
  const magnitude = 10 ** Math.floor(Math.log10(raw));
  for (const m of [1, 2, 4, 5, 10]) {
    if (m * magnitude >= raw) return m * magnitude;
  }
  return 10 * magnitude;
}

interface Props {
  buckets: MarkbadBucket[];
  seriesLabel: string;
}

export default function TimeSeriesChart({ buckets, seriesLabel }: Props) {
  const frameRef = useRef<HTMLDivElement>(null);
  const [width, setWidth] = useState(640);
  const [hoverIdx, setHoverIdx] = useState<number | null>(null);
  const [showTable, setShowTable] = useState(false);

  useEffect(() => {
    const el = frameRef.current;
    if (!el) return;
    const ro = new ResizeObserver((entries) => {
      const w = entries[0]?.contentRect.width;
      if (w && w > 80) setWidth(Math.floor(w));
    });
    ro.observe(el);
    return () => ro.disconnect();
  }, []);

  const data = useMemo(
    () => [...buckets].sort((a, b) => Date.parse(a.bucket) - Date.parse(b.bucket)),
    [buckets],
  );

  const plotW = Math.max(width - PAD_LEFT - PAD_RIGHT, 40);
  const plotH = HEIGHT - PAD_TOP;
  const yMax = niceMax(Math.max(0, ...data.map((d) => d.count)));

  const xAt = (i: number) =>
    PAD_LEFT + (data.length <= 1 ? plotW / 2 : (i / (data.length - 1)) * plotW);
  const yAt = (count: number) => PAD_TOP + plotH - (count / yMax) * plotH;

  const linePath = data.map((d, i) => `${i === 0 ? 'M' : 'L'}${xAt(i)},${yAt(d.count)}`).join(' ');
  const areaPath =
    data.length > 0
      ? `${linePath} L${xAt(data.length - 1)},${PAD_TOP + plotH} L${xAt(0)},${PAD_TOP + plotH} Z`
      : '';

  const yTicks = [0, yMax / 4, yMax / 2, (3 * yMax) / 4, yMax];

  // ~5 x labels, always including first and last bucket
  const xLabelIdx = useMemo(() => {
    if (data.length === 0) return [] as number[];
    const step = Math.max(1, Math.ceil(data.length / 5));
    const idx = new Set<number>();
    for (let i = 0; i < data.length; i += step) idx.add(i);
    idx.add(data.length - 1);
    return [...idx].sort((a, b) => a - b);
  }, [data]);

  const onMove = (e: React.PointerEvent<SVGSVGElement>) => {
    if (data.length === 0) return;
    const rect = e.currentTarget.getBoundingClientRect();
    const x = e.clientX - rect.left;
    const frac = (x - PAD_LEFT) / (plotW || 1);
    const idx = Math.round(frac * (data.length - 1));
    setHoverIdx(Math.min(data.length - 1, Math.max(0, idx)));
  };

  if (data.length === 0) {
    return <div className="empty">No mark-bad events recorded in the trailing 24 h.</div>;
  }

  const hovered = hoverIdx !== null ? data[hoverIdx] : null;

  return (
    <div>
      <div className="chart-frame" ref={frameRef}>
        <svg
          width={width}
          height={HEIGHT + AXIS_BAND}
          role="img"
          aria-label={`${seriesLabel}, hourly buckets over the trailing 24 hours`}
          onPointerMove={onMove}
          onPointerLeave={() => setHoverIdx(null)}
        >
          {/* horizontal gridlines: solid hairlines, recessive */}
          {yTicks.map((t) => (
            <g key={t}>
              <line
                x1={PAD_LEFT}
                x2={PAD_LEFT + plotW}
                y1={yAt(t)}
                y2={yAt(t)}
                stroke={t === 0 ? 'var(--baseline)' : 'var(--grid)'}
                strokeWidth={1}
                shapeRendering="crispEdges"
              />
              <text
                x={PAD_LEFT - 6}
                y={yAt(t) + 3.5}
                textAnchor="end"
                fontSize={10}
                fill="var(--muted)"
                style={{ fontVariantNumeric: 'tabular-nums' }}
              >
                {Number.isInteger(t) ? t.toLocaleString() : t.toFixed(1)}
              </text>
            </g>
          ))}

          {/* x-axis labels */}
          {xLabelIdx.map((i) => (
            <text
              key={i}
              x={xAt(i)}
              y={HEIGHT + 14}
              textAnchor="middle"
              fontSize={10}
              fill="var(--muted)"
              style={{ fontVariantNumeric: 'tabular-nums' }}
            >
              {hourLabel(data[i].bucket)}
            </text>
          ))}

          {/* area wash + line */}
          <path d={areaPath} fill="var(--series-1)" fillOpacity={0.1} />
          <path
            d={linePath}
            fill="none"
            stroke="var(--series-1)"
            strokeWidth={2}
            strokeLinejoin="round"
            strokeLinecap="round"
          />

          {/* crosshair + hovered marker (2px surface ring) */}
          {hovered !== null && hoverIdx !== null && (
            <g>
              <line
                x1={xAt(hoverIdx)}
                x2={xAt(hoverIdx)}
                y1={PAD_TOP}
                y2={PAD_TOP + plotH}
                stroke="var(--baseline)"
                strokeWidth={1}
                shapeRendering="crispEdges"
              />
              <circle
                cx={xAt(hoverIdx)}
                cy={yAt(hovered.count)}
                r={4}
                fill="var(--series-1)"
                stroke="var(--surface)"
                strokeWidth={2}
              />
            </g>
          )}
        </svg>

        {hovered !== null && hoverIdx !== null && (
          <div
            className="chart-tooltip"
            style={{
              left: Math.min(Math.max(xAt(hoverIdx), 60), width - 60),
              top: yAt(hovered.count) - 10,
            }}
          >
            <span className="val">{hovered.count.toLocaleString()}</span>{' '}
            <span className="lbl">
              {seriesLabel} · {hourLabel(hovered.bucket)}
            </span>
          </div>
        )}
      </div>

      <div className="chart-actions">
        <button className="link" onClick={() => setShowTable((s) => !s)}>
          {showTable ? 'Hide table' : 'Table view'}
        </button>
      </div>

      {showTable && (
        <div className="table-wrap chart-table">
          <table>
            <thead>
              <tr>
                <th>Hour bucket</th>
                <th className="num">{seriesLabel}</th>
              </tr>
            </thead>
            <tbody>
              {data.map((d) => (
                <tr key={d.bucket}>
                  <td>{absoluteTime(d.bucket)}</td>
                  <td className="num">{d.count.toLocaleString()}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  );
}
