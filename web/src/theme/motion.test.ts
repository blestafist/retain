import { describe, expect, it } from 'vitest';
import {
  getMotionTransition,
  motionSprings,
  reducedMotionCrossfade,
  reducedMotionCrossfadeVariants,
} from './motion';

describe('MD3 Expressive motion tokens', () => {
  it('provides distinct spatial and effects springs for each speed', () => {
    for (const category of ['spatial', 'effects'] as const) {
      expect(motionSprings[category].fast.type).toBe('spring');
      expect(motionSprings[category].default.type).toBe('spring');
      expect(motionSprings[category].slow.type).toBe('spring');
      expect(motionSprings[category].fast).not.toEqual(
        motionSprings[category].default,
      );
      expect(motionSprings[category].default).not.toEqual(
        motionSprings[category].slow,
      );
      expect(motionSprings[category].fast.stiffness)
        .toBeGreaterThan(motionSprings[category].default.stiffness);
      expect(motionSprings[category].default.stiffness)
        .toBeGreaterThan(motionSprings[category].slow.stiffness);
    }
  });

  it('substitutes a short crossfade when reduced motion is requested', () => {
    expect(getMotionTransition('spatial', 'slow', true))
      .toBe(reducedMotionCrossfade);
    expect(getMotionTransition('effects', 'fast', false))
      .toEqual(motionSprings.effects.fast);
    expect(reducedMotionCrossfadeVariants.initial).toEqual({ opacity: 0 });
    expect(reducedMotionCrossfadeVariants.animate).toMatchObject({ opacity: 1 });
    expect(reducedMotionCrossfadeVariants.exit).toMatchObject({ opacity: 0 });
    expect(reducedMotionCrossfade.duration).toBeLessThanOrEqual(0.12);
  });
});
