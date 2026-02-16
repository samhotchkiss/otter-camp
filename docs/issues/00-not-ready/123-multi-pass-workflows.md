# 🚫 NOT READY — This is a future vision spec, not an active issue

# Issue #123: Multi-Pass Workflows

## Summary

Enable multi-agent, multi-pass workflows where work products move through a defined pipeline of agents, with review loops and handoffs. This is the content production pipeline (and any similar sequential collaboration pattern) as a first-class OtterCamp primitive.

## Motivation

Real work isn't single-agent. A blog post doesn't go from idea to published in one step. It flows:

1. **SEO Specialist** → identifies topics, keywords, content gaps
2. **Blog Writer** → drafts the post
3. **Editor** → reviews for quality, tone, style
4. **SEO Specialist** → reviews for keyword coverage, structure, meta
5. **Designer** → adds imagery, graphics, featured image
6. **Editor** → final review and publish

Each step has a different agent, different skills, different review criteria. The workflow needs to:
- Move work forward automatically when a step is approved
- Route back to a previous step when revisions are needed
- Track which pass the work is on and who's touched it
- Preserve context across handoffs (each agent needs to see what prior agents did)

## Requirements

### Pipeline Definition
- Define a workflow as an ordered sequence of **stages**
- Each stage has: an assigned agent role, input expectations, output expectations, and review criteria
- Stages can be **linear** (A → B → C) or have **review loops** (C → back to B if rejected)
- Support conditional stages (e.g., "Designer stage only if post includes data visualizations")

### Handoffs
- When an agent completes a stage, the work product moves to the next stage automatically
- The next agent receives: the work product, the prior agent's notes, and any review feedback
- Context is preserved via the issue/project — agents read the commit history and comments

### Review Loops
- Any stage can **approve** (move forward) or **reject** (send back with feedback)
- Rejection targets a specific prior stage, not just "the previous one"
- Rejection includes structured feedback: what's wrong, what "done" looks like
- Track loop count to prevent infinite cycles (configurable max, default 3)

### Visibility
- Pipeline status visible in the OtterCamp UI — which stage, who's working, how many passes
- Timeline view showing each handoff, approval, and rejection
- Estimated completion based on average stage duration

### Templates
- Pre-built pipeline templates for common workflows:
  - **Blog Post**: SEO → Writer → Editor → SEO Review → Designer → Final Review → Publish
  - **Code Feature**: PM (spec) → Architect (design) → Engineer (build) → Code Reviewer → QA → Designer (UI review) → Merge
  - **Social Media**: Strategist → Writer → Designer → Brand Review → Schedule
- Custom pipeline builder for user-defined workflows

## Example: Blog Post Pipeline

```yaml
workflow: blog-post-pipeline
stages:
  - name: topic-research
    agent_role: seo-specialist
    output: topic brief (keyword, angle, target audience, competing content)
    
  - name: draft
    agent_role: blog-writer
    input: topic brief
    output: draft post (markdown)
    
  - name: editorial-review
    agent_role: editor
    input: draft post
    output: approved draft or revision notes
    reject_to: draft
    
  - name: seo-review
    agent_role: seo-specialist
    input: approved draft
    output: SEO-approved draft or revision notes
    reject_to: draft
    
  - name: design
    agent_role: visual-designer
    input: SEO-approved draft
    output: draft with imagery
    
  - name: final-review
    agent_role: editor
    input: draft with imagery
    output: publish-ready post
    reject_to: design
    
  - name: publish
    agent_role: content-strategist
    action: publish to platform
```

## Relationship to Existing Specs

- **#105 (Pipeline Spec)**: This extends #105's Planner → Worker → Reviewer lifecycle to support arbitrary multi-stage pipelines with more than 3 roles
- **#110 (Chameleon Architecture)**: Each stage activates a different Chameleon identity
- **#111 (Memory/Ellie)**: Cross-stage context preservation relies on Elephant for handoff memory

## Open Questions

- Should pipelines be project-level or org-level? (Probably both — templates at org, instances at project)
- How does this interact with OtterCamp's issue system? One issue per pipeline run? Or one issue per stage?
- Can stages run in parallel? (e.g., Designer and SEO Specialist review simultaneously)
- How do we handle pipeline runs that stall? Auto-escalation after timeout?
