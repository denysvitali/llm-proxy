import { motion, useReducedMotion } from 'framer-motion'
import type { ReactNode } from 'react'
import { springGentle } from '../motion/tokens'

// The loading veil. While a page's primary queries settle, content stays
// mounted (layout never jumps) but drops to a dimmed, slightly blurred state
// behind a thin indeterminate rail. The rail is decorative; the page's own
// skeletons and role="status" nodes carry the accessible loading signal.
export default function Fade({ pending, children }: { pending: boolean; children: ReactNode }) {
  const reduced = useReducedMotion() ?? false
  return (
    <div className="fade-wrap" data-pending={pending || undefined}>
      {pending && (
        <div className="fade-progress" aria-hidden="true">
          <motion.span
            className="fade-progress-bar"
            initial={{ x: '-40%', width: '40%' }}
            animate={reduced ? { opacity: 0 } : { x: ['-40%', '100%'] }}
            transition={reduced ? { duration: 0 } : { duration: 1.25, repeat: Infinity, ease: [0.4, 0, 0.2, 1] }}
          />
        </div>
      )}
      <motion.div
        className="fade-body"
        initial={false}
        animate={{
          opacity: pending ? 0.5 : 1,
          filter: pending && !reduced ? 'blur(1.5px)' : 'blur(0px)',
        }}
        transition={reduced ? { duration: 0 } : springGentle}
      >
        {children}
      </motion.div>
    </div>
  )
}
