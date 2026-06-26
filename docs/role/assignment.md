# Technical Assessment

## Overview

**Are you our next engineering manager?**

The following is an assignment that serves as the foundation of a good and in depth technical interview, in which you will get a better understanding of the company and we learn more about your technical capabilities. 

**Time Estimate:** No more than 4 hours

**Format:** You do not have to finish the test in a chronological order, so start with answering the questions within your strongest capabilities. This is a somewhat different engineering test where you get the chance of proving a wide variety of skills. 

If some of the questions lie way outside your area of expertise, see how far you get in answering the question, or simply leave the question unanswered. If you do not manage to finalize the entire test within the time frame, please prepare well-documented thoughts on how you would have solved the remaining work prior to the technical interview.

### Submission Requirements

Send us your submission via email. The submission should include:

- **PDF file** with your report, including your textual responses, along with any tables or figures
- **ZIP (or similar) archive** - If you wrote code to generate some responses, please do include the code as well

---

## Task 1: A Good Old Messaging System

### Overview

**Task:** Queue between a file reader and writer

It's your job to design a simple messaging system by using a queue service:

- Read lines from a file
- Write lines to a queue service via a network protocol
- Read lines from the queue service via a network protocol
- Write lines to a file

Implement it as multiple asynchronous workers exchanging information through a service.

### Requirements

- Documentation on how to run your solution
- Queuing service needs to be written with only stdlib of your language of choice
- An arbitrary ASCII text file fed into the solution should produce an identical copy
