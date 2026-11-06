import { motion } from 'framer-motion'
import type { ReactNode } from 'react'
import { useMotionPreset } from '../../motion/tokens'

// Mount reveal for each Overview band: a short fade, staggered by 40ms down the
// page. Deliberately no translation: a band that slides in moves everything
// below it while it settles, which reads as the page twitching when live data
// lands. Opacity alone never changes layout, so the page can update in place.
export default function OverviewReveal({ index = 0, children }: { index?: number; children: ReactNode }) {
  const { reduced } = useMotionPreset()
  return (
    <motion.div
      className="overview-reveal"
      initial={reduced ? false : { opacity: 0 }}
      animate={{ opacity: 1 }}
      transition={reduced ? { duration: 0 } : { duration: 0.28, ease: [0.22, 1, 0.36, 1], delay: Math.min(index, 6) * 0.04 }}
    >
      {children}
    </motion.div>
  )
}
