import type { Transition, Variants } from 'motion/react';

export type MotionSpeed = 'fast' | 'default' | 'slow';
export type MotionCategory = 'spatial' | 'effects';

type SpringSet = Record<MotionSpeed, Transition>;

export const motionSprings = {
  spatial: {
    fast: { type: 'spring', stiffness: 520, damping: 42, mass: 0.8 },
    default: { type: 'spring', stiffness: 320, damping: 34, mass: 1 },
    slow: { type: 'spring', stiffness: 220, damping: 30, mass: 1.15 },
  },
  effects: {
    fast: { type: 'spring', stiffness: 800, damping: 70, mass: 0.7 },
    default: { type: 'spring', stiffness: 600, damping: 55, mass: 0.85 },
    slow: { type: 'spring', stiffness: 420, damping: 42, mass: 1 },
  },
} satisfies Record<MotionCategory, SpringSet>;

export const reducedMotionCrossfade: Transition = {
  type: 'tween',
  duration: 0.12,
  ease: 'easeOut',
};

export const reducedMotionCrossfadeVariants: Variants = {
  initial: { opacity: 0 },
  animate: { opacity: 1, transition: reducedMotionCrossfade },
  exit: { opacity: 0, transition: reducedMotionCrossfade },
};

export function getMotionTransition(
  category: MotionCategory,
  speed: MotionSpeed,
  prefersReducedMotion: boolean,
): Transition {
  return prefersReducedMotion
    ? reducedMotionCrossfade
    : motionSprings[category][speed];
}
