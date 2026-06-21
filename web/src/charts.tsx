// Chart wrappers over recharts (replaces @mantine/charts).
import {
  Area,
  AreaChart as RAreaChart,
  CartesianGrid,
  Cell,
  Legend,
  Pie,
  PieChart as RPieChart,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from 'recharts'

const BLUE = '#0d4cd3'
const TEAL = '#0d9488'

type Point = { day: string; up: number; down: number }

export function TrafficArea({
  data,
  height = 260,
  fmt,
}: {
  data: Point[]
  height?: number
  fmt: (n: number) => string
}) {
  return (
    <ResponsiveContainer width="100%" height={height}>
      <RAreaChart data={data} margin={{ top: 8, right: 8, left: 0, bottom: 0 }}>
        <defs>
          <linearGradient id="gDown" x1="0" y1="0" x2="0" y2="1">
            <stop offset="0%" stopColor={TEAL} stopOpacity={0.35} />
            <stop offset="100%" stopColor={TEAL} stopOpacity={0} />
          </linearGradient>
          <linearGradient id="gUp" x1="0" y1="0" x2="0" y2="1">
            <stop offset="0%" stopColor={BLUE} stopOpacity={0.35} />
            <stop offset="100%" stopColor={BLUE} stopOpacity={0} />
          </linearGradient>
        </defs>
        <CartesianGrid strokeDasharray="3 3" stroke="#eef2f7" vertical={false} />
        <XAxis dataKey="day" tick={{ fontSize: 12, fill: '#8995a5' }} tickLine={false} axisLine={false} />
        <YAxis tickFormatter={fmt} tick={{ fontSize: 11, fill: '#8995a5' }} tickLine={false} axisLine={false} width={56} />
        <Tooltip
          formatter={(v: number, n) => [fmt(v), n === 'down' ? 'Принято' : 'Отдано']}
          contentStyle={{ borderRadius: 12, border: '1px solid #eef2f7', fontSize: 13 }}
        />
        <Legend
          formatter={(v) => (v === 'down' ? 'Принято' : 'Отдано')}
          iconType="circle"
          wrapperStyle={{ fontSize: 13 }}
        />
        <Area type="monotone" dataKey="down" stroke={TEAL} fill="url(#gDown)" strokeWidth={2} />
        <Area type="monotone" dataKey="up" stroke={BLUE} fill="url(#gUp)" strokeWidth={2} />
      </RAreaChart>
    </ResponsiveContainer>
  )
}

export function TrafficDonut({
  data,
  size = 240,
  fmt,
  centerLabel,
}: {
  data: { name: string; value: number; color: string }[]
  size?: number
  fmt: (n: number) => string
  centerLabel?: string
}) {
  return (
    <div style={{ position: 'relative', width: size, height: size }}>
      <ResponsiveContainer width="100%" height="100%">
        <RPieChart>
          <Tooltip
            formatter={(v: number, n) => [fmt(v), n]}
            contentStyle={{ borderRadius: 12, border: '1px solid #eef2f7', fontSize: 13 }}
          />
          <Pie
            data={data}
            dataKey="value"
            nameKey="name"
            innerRadius="62%"
            outerRadius="100%"
            paddingAngle={1}
            stroke="none"
          >
            {data.map((d, i) => (
              <Cell key={i} fill={d.color} />
            ))}
          </Pie>
        </RPieChart>
      </ResponsiveContainer>
      {centerLabel && (
        <div className="pointer-events-none absolute inset-0 flex items-center justify-center text-sm font-semibold text-ink">
          {centerLabel}
        </div>
      )}
    </div>
  )
}
