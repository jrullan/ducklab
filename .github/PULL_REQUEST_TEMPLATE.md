## What changed

<!-- One line. The body says WHY it needed changing — ideally with the
incident that proved it, like every commit message in this repo. -->

## Checklist

- [ ] `make` is green (vet, Go tests, frontend build)
- [ ] Frontend touched: `npx tsc --noEmit && npx vitest run` green
- [ ] Routes touched: `make api` ran; generated files not hand-edited
- [ ] Flipped behaviour: the pinning test is rewritten with the new
      reasoning, not deleted
- [ ] Comments state incidents/constraints, not the next line
