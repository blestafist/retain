import { motion, useReducedMotion, type HTMLMotionProps } from 'motion/react';
import { getMotionTransition } from '../theme/motion';
import { target } from './shared';

type Props = HTMLMotionProps<'button'> & { variant?: 'filled' | 'tonal' };
export function Button({ variant = 'filled', className = '', type = 'button', ...props }: Props) {
  const reduced = !!useReducedMotion();
  return <motion.button {...props} type={type} className={`${target} inline-flex items-center justify-center px-5 disabled:opacity-50 ${variant === 'filled' ? 'bg-primary text-on-primary' : 'bg-secondary-container text-on-secondary-container'} ${className}`} initial={false} animate={{ borderRadius: '999px' }} whileTap={reduced ? undefined : { borderRadius: '0.5rem' }} transition={getMotionTransition(reduced ? 'effects' : 'spatial', 'fast', reduced)} />;
}
