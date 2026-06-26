# Engineering Manager Technical Assessment

## Overview

This assignment is designed to evaluate your technical capabilities and engineering judgment. Through implementing a distributed messaging system, we assess your ability to:
- Design scalable systems with concurrent components
- Write maintainable, well-tested code
- Document technical decisions clearly
- Balance pragmatism with best practices

**Time Estimate:** 3-4 hours  
**Format:** You may work on tasks in any order that suits your strengths

### Submission Guidelines

Your submission must include:

1. **Code Archive** (ZIP/tar.gz)
   - Source code with build/run instructions
   - Any test files and data
   - Architecture documentation (README, diagrams, decision notes)

2. **Report (PDF)**
   - Overview of your implementation approach
   - Key design decisions and trade-offs
   - Any incomplete sections with documented approach for completion
   - Instructions for building, testing, and running your solution

---

## Task 1: Asynchronous File-to-File Message Queue

### Problem Statement

Design and implement a **distributed messaging system** consisting of:
- A **file reader** worker that reads lines from an input file
- A **queue service** that stores messages and handles network communication
- A **file writer** worker that writes lines to an output file

All components communicate asynchronously through a network protocol. The system should demonstrate proper concurrency handling, error recovery, and clean architecture.

### Detailed Requirements

#### Functional Requirements
1. **File Reader Worker**
   - Reads lines from an arbitrary ASCII text file (one line per message)
   - Sends each line to the queue service via network protocol
   - Must handle files with varying line lengths and line ending styles (\n, \r\n, etc.)

2. **Queue Service**
   - Implements a thread-safe in-memory message queue
   - Exposes a network interface for producers and consumers
   - Maintains message ordering (FIFO)
   - Must be built using **only standard library of your chosen language**
   - Handles concurrent reader and writer connections

3. **File Writer Worker**
   - Receives messages from queue service via network protocol
   - Writes each message as a line to an output file
   - Preserves line ordering and content exactly

#### Non-Functional Requirements
- **Concurrency:** Workers must operate asynchronously and independently
- **Fidelity:** An input ASCII file must produce an identical copy on output (byte-for-byte)
- **Robustness:** System should handle graceful shutdown and basic error conditions
- **Code Quality:** Production-grade code with error handling, logging, and tests

#### Constraints
- **Standard Library Only:** Queue service uses only built-in language libraries (no external frameworks)
- **Protocol Design:** You choose the network protocol (TCP, HTTP, etc.); document your choice
- **Language:** Choose any modern language (Go, Rust, Python, Java, C++, etc.)

### Acceptance Criteria

✓ Solution compiles and runs without external dependencies (beyond stdlib)  
✓ Reader → Queue → Writer pipeline successfully transfers file content  
✓ Output file is identical to input file (exact byte match)  
✓ System handles multiple lines correctly  
✓ Graceful handling of edge cases (empty files, large files, special characters)  
✓ Architecture supports independent, testable components  
✓ Code is documented and tests are provided  

### Evaluation Criteria

Your solution will be evaluated on:

1. **Correctness** (30%)
   - Does it work end-to-end?
   - Are edge cases handled?
   - Is the file transfer accurate?

2. **Code Quality** (25%)
   - Is the code readable and maintainable?
   - Are concerns properly separated?
   - Is error handling appropriate?

3. **Architecture & Design** (20%)
   - Is the system scalable?
   - Are workers genuinely asynchronous?
   - Is the protocol choice well-justified?

4. **Documentation** (15%)
   - Are setup and execution instructions clear?
   - Are design decisions explained?
   - Is the code well-commented?

5. **Testing** (10%)
   - Are there automated tests?
   - Do tests cover key functionality?

### Suggestions for Improvement (Optional)

If you finish the base requirements, consider:
- Message acknowledgment / at-least-once delivery semantics
- Queue persistence (write to disk)
- Metrics collection (throughput, latency)
- Multiple reader/writer support
- Backpressure handling

---

## Implementation Notes

- **You don't need to finish everything.** Partial solutions with clear documentation of remaining work are acceptable and will be discussed in the interview.
- **Show your thinking.** Well-documented incomplete work demonstrates engineering judgment.
- **Focus on your strengths.** If systems design isn't your forte, show strong testing and code quality instead.
