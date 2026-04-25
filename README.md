# Valory

Valory is an AI professor system that creates personalized lesson plans, course content, homework assignments, and grading for any topic — delivered through a structured multi-agent architecture.

---

## Screenshots

> _Screenshots and demo coming soon._

---

## What is Valory?

Valory gives you a structured, high-quality learning experience powered by AI. Unlike asking a chatbot a question, Valory builds a full course: a syllabus, sequenced lessons, practice problems, graded homework, and a final project — all tailored to your goals. Content is sourced from reputable references and research to ensure accuracy above all else.

---

## How It Works

### The Learning Flow

1. You tell Valory what you want to learn (e.g., "Teach me multivariable calculus")
2. The **University Chair** asks you questions to understand your goals and background
3. The Chair orchestrates agents to produce a **syllabus and course outline**, including projects, homework, and a final project
4. You review and approve the lesson plan
5. The Chair spawns agents to generate **lesson content and assignments**, managing task dependencies so work is sequenced correctly
6. You study the content and interact with the Chair via chat to ask questions
7. You submit homework; agents **grade it** and return constructive feedback
8. You submit a final project and receive an overall course grade

### Multi-Agent Architecture

```
+----------------+
| Student (User) |
+-------+--------+
        |
        v
+-------------------------------+
| University Chair              |
| Orchestrator                  |
+-------+-----------------------+
        |
        | spawns task workers
        v
+--------------------------------------------------+
| Professors                                       |
|  +-------------+  +-------------+  +----------+ |
|  | Professor 1 |  | Professor 2 |  | Prof. N  | |
|  +------+------+  +------+------+  +-----+----+ |
+---------|----------------|-----------------|-----+
          v                v                 v
+--------------------------------------------------+
| Advisors / Verifiers                             |
|  +-----------+    +-----------+    +-----------+ |
|  | Advisor 1 |    | Advisor 2 |    | Advisor N | |
|  +-----+-----+    +-----+-----+    +-----+-----+ |
+--------|----------------|-----------------|------+
         |                |                 |
         v                v                 v
+--------------------------------------------------+
| If failed: return to Professor for correction,  |
| then re-verify                                   |
+-------------------------+------------------------+
                          |
                          v
+--------------------------------------------------+
| Chief Advisor                                    |
| Final cross-cutting quality and correctness gate |
+-------------------------+------------------------+
                          |
                          v
+----------------+
| Output         |
| to Student     |
+----------------+
```

**Failure loop:** If an Advisor rejects a Professor's work, it is sent back for correction and re-reviewed before reaching the Chief Advisor.

---

## Features

- **Custom course generation** for any topic
- **Syllabus and lesson planning** with student approval before content is generated
- **Sequenced lesson content** with real examples and practice problems
- **Homework assignments** with due dates and a 5% per-day late deduction
- **AI grading** with constructive feedback
- **Final project** and overall course grade
- **Badges and rewards** for quality work and on-time submissions
- **Content library** — generated courses are saved and reused for future students
- **AsciiDoc output** rendered in the web UI, exportable to PDF or standalone HTML
- **Chat interface** for interacting with the University Chair throughout the course
- **Admin and student roles** with authentication

---

## Tech Stack

| Layer | Technology |
|---|---|
| Backend | Go |
| Frontend | Vue.js |
| AI | Anthropic SDK (Claude) |
| Database | PostgreSQL |
| Infrastructure | Docker |

---

## Getting Started

### Prerequisites

- [Docker](https://www.docker.com/) installed
- An [Anthropic](https://www.anthropic.com/) account with API access (API key or OAuth)

### Installation

```bash
# 1. Clone the repository
git clone https://github.com/robinonsay/valory.git
cd valory

# 2. Configure your environment
cp .env.example .env
# Edit .env and add your Anthropic API key

# 3. Start the application
docker compose up

# 4. Open your browser
# Navigate to http://localhost:3000
```

---

## Content Library

When a student completes a course on a topic, the generated AsciiDoc content is saved to a shared library. Future students requesting the same topic will receive the existing content rather than regenerating it from scratch. Each course document is capped at 500 lines and composed via AsciiDoc includes for easy reuse and maintenance.

---

## Grading

| Item | Details |
|---|---|
| Course total | 100 points |
| Late penalty | 5% deduction per day |
| Feedback | Constructive written feedback on every submission |
| Rewards | Badges awarded for quality work and on-time submissions |

---

## Contributing

Contributions are welcome! To get started:

1. Fork the repository
2. Open an issue describing the change you want to make before submitting a PR
3. Submit a pull request against `main`

Please follow the existing code style and ensure all tests pass before submitting.

---

## License

[MIT](LICENSE)
