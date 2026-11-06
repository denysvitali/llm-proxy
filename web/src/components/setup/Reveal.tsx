import { motion } from 'framer-motion'
import type { ReactNode } from 'react'
import { springSmooth, useMotionPreset } from '../../motion/tokens'

// A restrained mount reveal: an 8px rise plus fade, staggered by the caller.
// All motion decisions go through useMotionPreset so prefers-reduced-motion
// collapses it to a plain render everywhere in the app, never per-component.
export function Reveal({ children, delay = 0, className }: {
  children: ReactNode
  delay?: number
  className?: string
}) {
  const { reduced } = useMotionPreset()
  return (
    <motion.div
      className={className}
      style={{ minWidth: 0 }}
      initial={reduced ? false : { opacity: 0, y: 8 }}
      animate={{ opacity: 1, y: 0 }}
      transition={reduced ? { duration: 0 } : { ...springSmooth, delay }}
    >
      {children}
    </motion.div>
  )
}
