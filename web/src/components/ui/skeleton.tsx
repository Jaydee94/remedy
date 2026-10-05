import { cn } from "cn"

function Skeleton({ className, ...props }: React.ComponentProps<"div">) {
  return (
    <div
      data-slot="skeleton"
      className={cn("animate-rm-pulse rounded-2xl bg-card", className)}
      {...props}
    />
  )
}

export { Skeleton }
