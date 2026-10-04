// Small status badge. Tone is derived from well-known values so status
// colors always ride WITH the text label, never alone.

const TONES: Record<string, string> = {
  // device / bundle / rollout status
  active: 'good',
  retired: 'bad',
  deprecated: 'warn',
  draft: 'info',
  paused: 'warn',
  completed: 'good',
  aborted: 'bad',
  // update outcomes
  ok: 'good',
  fail: 'bad',
  rollback: 'bad',
  none: '',
  // event types
  checkin: '',
  offer: 'info',
  download: 'info',
  'install-started': 'info',
  'install-ok': 'good',
  'install-fail': 'bad',
  enroll: 'info',
  revoke: 'bad',
};

export default function Badge({ value }: { value: string }) {
  const tone = TONES[value] ?? '';
  return <span className={tone ? `badge ${tone}` : 'badge'}>{value}</span>;
}
