import { Box } from '@mantine/core'
import type { ReactNode } from 'react'

export default function Fade({ pending, children }: { pending: boolean; children: ReactNode }) {
  return <Box style={{ opacity: pending ? 0.65 : 1, transition: 'opacity 200ms' }}>{children}</Box>
}
