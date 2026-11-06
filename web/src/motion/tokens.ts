// Motion system, ported from easyui.site's motion-tokens (stiffness / damping /
// mass presets) and adapted for framer-motion 14. The point of the spring
// vocabulary is consistency: every surface should settle with the same
// physical feel, so hover feedback is snappy, panels are gentle, and shared
// layout moves (tab pills, pagination) feel continuous.
//
// Reduced motion is handled centrally (see useMotionPreset below), never
// per-component ad hoc. Anything decorative must be skipped when the user
// asked for less motion.
import type { Transition, Variants } from 'framer-motion'
import { useReducedMotion } from 'framer-motion'

export const springGentle: Transition = { type: 'spring', stiffness: 170, damping: 26, mass: 0.9 }
export const springSmooth: Transition = { type: 'spring', stiffness: 200, damping: 28, mass: 0.85 }
export const springSnappy: Transition = { type: 'spring', stiffness: 380, damping: 30, mass: 0.5 }
export const springMorph: Transition = { type: 'spring', stiffness: 220, damping: 32, mass: 1 }
export const springResponsive: Transition = { type: 'spring', stiffness: 320, damping: 24, mass: 0.7 }
export const springSoft: Transition = { type: 'spring', stiffness: 150, damping: 22, mass: 0.9 }

// Property transitions (color, opacity) — EasyUI's "emphasized" curves.
export const easeSoft: Transition = { duration: 0.32, ease: [0.22, 1, 0.36, 1] }
export const easeFast: Transition = { duration: 0.2, ease: [0.2, 0, 0, 1] }
export const EASE_OUT = [0.23, 1, 0.32, 1] as const

// A page/section reveal: barely-there rise plus fade. Long enough to read as
// intentional, short enough that it never delays content.
export const revealVariants: Variants = {
  hidden: { opacity: 0, y: 8 },
  visible: { opacity: 1, y: 0 },
}

// Shared transition and offset for mount reveals. Reveal.tsx (and any lane
// hand-rolling one) reads these instead of retyping the spring and the 8px
// rise, so all entrance motion settles identically.
export const revealTransition: Transition = springSmooth
export const REVEAL_Y = 8

// Staggering children of a revealed section (cards in a grid, rows in a list).
export const staggerParent: Variants = {
  hidden: {},
  visible: { transition: { staggerChildren: 0.045, delayChildren: 0.02 } },
}

export const staggerChild: Variants = {
  hidden: { opacity: 0, y: 10 },
  visible: { opacity: 1, y: 0, transition: springSmooth },
}

// Centralized reduced-motion decision. Components call this instead of
// checking the media query themselves, so behavior can't drift between pages.
export function useMotionPreset() {
  const reduced = useReducedMotion() ?? false
  return {
    reduced,
    /** A transition that is instant when reduced motion is requested. */
    transition: (motion: Transition): Transition => (reduced ? { duration: 0 } : motion),
    /** Reveal variants, disabled under reduced motion. */
    reveal: reduced ? undefined : revealVariants,
    stagger: reduced ? undefined : staggerParent,
    child: reduced ? undefined : staggerChild,
  }
}
