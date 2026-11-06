import { AnimatePresence, motion } from 'framer-motion'
import { useMotionPreset } from '../motion/tokens'

// A slim, non-blocking route progress bar. It starts full-width on navigation
// and fades out, so the transition reads as "the page is loading" without
// pretending to know real progress. Purely decorative: aria-hidden.
export default function RouteProgress({ routeKey }: { routeKey: string }) {
  const { reduced } = useMotionPreset()
  if (reduced) return null
  return (
    <div className="route-progress" aria-hidden="true">
      <AnimatePresence mode="popLayout" initial={false}>
        <motion.span
          key={routeKey}
          className="route-progress-bar"
          initial={{ scaleX: 0, opacity: 1 }}
          animate={{ scaleX: 1, opacity: 0 }}
          transition={{ scaleX: { duration: 0.45, ease: [0.23, 1, 0.32, 1] }, opacity: { duration: 0.2, delay: 0.25 } }}
        />
      </AnimatePresence>
    </div>
  )
}
