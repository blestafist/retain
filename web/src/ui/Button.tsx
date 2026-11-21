import { motion, useReducedMotion, type HTMLMotionProps } from 'motion/react';
import { getMotionTransition } from '../theme/motion';
import { target } from './shared';

type Props = HTMLMotionProps<'button'> & { variant?: 'filled' | 'tonal' };
export function Button({ variant = 'filled', className = '', type = 'button', ...props }: Props) {
  const reduced = !!useReducedMotion();
  return <motion.button {...props} type={type}
    className={`${target} material-button inline-flex items-center justify-center gap-2 rounded-full px-5 disabled:cursor-not-allowed disabled:opacity-50 ${variant === 'filled' ? 'bg-primary text-on-primary' : 'bg-secondary-container text-on-secondary-container'} ${className}`}
    whileTap={reduced || props.disabled ? undefined : { borderRadius: '0.5rem' }}
    transition={getMotionTransition('spatial', 'fast', reduced)} />;
}
