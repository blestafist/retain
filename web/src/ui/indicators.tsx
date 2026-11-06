import { motion, useReducedMotion } from 'motion/react';
import { t, type TranslationKey } from '../i18n/en';
import { getMotionTransition } from '../theme/motion';

const paths = {
  cookie: 'M50 4C57 4 60 12 67 10C74 8 78 15 80 21C82 27 91 28 92 36C93 43 87 48 91 55C95 62 90 68 84 71C78 74 80 83 73 87C67 91 61 86 55 91C49 96 42 92 38 87C34 82 26 86 21 81C16 76 19 69 13 65C7 61 7 54 11 48C15 42 9 36 13 29C17 23 24 24 27 17C30 10 37 9 43 11C46 12 48 4 50 4Z',
  clover: 'M50 20C28 -3 4 19 24 50C4 81 28 103 50 80C72 103 96 81 76 50C96 19 72 -3 50 20Z',
  sunny: 'M50 3 58 20 72 9 74 29 93 27 83 44 98 55 79 61 84 80 65 75 56 96 46 79 29 91 27 72 7 73 19 57 3 46 22 40 17 21 36 26Z',
};
export function DecorativeShape({ shape = 'cookie', className = '' }: { shape?: keyof typeof paths; className?: string }) {
  return <svg aria-hidden="true" viewBox="0 0 100 100" className={className}><path fill="currentColor" d={paths[shape]} /></svg>;
}
export function LoadingIndicator({ label = 'loading' }: { label?: TranslationKey }) {
  const reduced = !!useReducedMotion();
  return <span role="status" aria-label={t(label)} className="inline-flex h-12 w-12 items-center justify-center text-primary"><motion.svg aria-hidden="true" viewBox="0 0 100 100" className="h-10 w-10" animate={reduced ? { opacity: 1 } : { rotate: 90 }} transition={reduced ? getMotionTransition('effects', 'default', true) : { ...getMotionTransition('spatial', 'slow', false), repeat: Infinity, repeatType: 'reverse' }}><motion.path fill="currentColor" initial={{ d: paths.sunny }} animate={reduced ? { d: paths.sunny } : { d: 'M50 3 60 13 72 9 77 23 93 27 87 41 98 55 83 62 84 80 68 80 56 96 45 85 29 91 24 76 7 73 14 57 3 46 18 38 17 21 34 22Z' }} transition={reduced ? getMotionTransition('effects', 'default', true) : { ...getMotionTransition('spatial', 'slow', false), repeat: Infinity, repeatType: 'reverse' }} /></motion.svg></span>;
}
export function WavyProgress({ value, label = 'progress' }: { value: number; label?: TranslationKey }) {
  const safe = Number.isFinite(value) ? Math.max(0, Math.min(100, value)) : 0;
  return <div role="progressbar" aria-label={t(label)} aria-valuemin={0} aria-valuemax={100} aria-valuenow={safe} className="relative h-6 w-full overflow-hidden"><svg aria-hidden="true" preserveAspectRatio="none" viewBox="0 0 100 16" className="absolute inset-0 h-full w-full text-secondary-container"><path d="M0 8 Q2.5 0 5 8 T10 8 T15 8 T20 8 T25 8 T30 8 T35 8 T40 8 T45 8 T50 8 T55 8 T60 8 T65 8 T70 8 T75 8 T80 8 T85 8 T90 8 T95 8 T100 8" fill="none" stroke="currentColor" strokeWidth="3" /></svg><div className="absolute inset-y-0 left-0 overflow-hidden" style={{ width: `${safe}%` }}><svg aria-hidden="true" preserveAspectRatio="none" viewBox="0 0 100 16" className="h-full text-primary" style={{ width: safe ? `${10000 / safe}%` : '0%' }}><path d="M0 8 Q2.5 0 5 8 T10 8 T15 8 T20 8 T25 8 T30 8 T35 8 T40 8 T45 8 T50 8 T55 8 T60 8 T65 8 T70 8 T75 8 T80 8 T85 8 T90 8 T95 8 T100 8" fill="none" stroke="currentColor" strokeWidth="3" /></svg></div></div>;
}
